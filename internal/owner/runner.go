// Package owner runs external owner commands. It never uses a shell and never
// builds a command from user input: callers pass an exact argv from the catalog
// (invariant I4).
//
// Provenance: independent reimplementation of Mole's command discipline (exact
// argv, bounded timeouts, per-user vs elevated routing).
package owner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Result is the outcome of a command run.
type Result struct {
	Argv     []string
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
	DryRun   bool
	Duration time.Duration
}

// Runner executes owner commands with dry-run awareness and bounded timeouts.
type Runner struct {
	// DryRun, when true, computes the argv without executing.
	DryRun bool
	// Env is the environment for child processes. nil inherits the current one.
	Env []string
	// Dir is the working directory. "" inherits the current one.
	Dir string
	// Log, if set, receives a line per executed command.
	Log func(string)
	// UserCredential, when set, makes a per-user command (elevate=false) run
	// with the invoking user's credentials when the process is elevated
	// (euid 0). This is how spec §7.6 is enforced: user state must never run
	// with inherited root credentials. It is ignored for elevated commands.
	UserCredential *syscall.Credential
	// UserEnv is the invoking user's HOME/XDG environment. It is overlaid on
	// the child environment for per-user commands so they operate on the same
	// trees burrow resolved, never root's.
	UserEnv []string
	// Geteuid is injectable for tests; it defaults to os.Geteuid.
	Geteuid func() int
}

// New returns a Runner.
func New(dryRun bool) *Runner { return &Runner{DryRun: dryRun} }

func (r *Runner) euid() int {
	if r.Geteuid != nil {
		return r.Geteuid()
	}
	return os.Geteuid()
}

// userSysProcAttr returns the process attributes for a per-user command. It is
// nil unless the command must drop from root to the invoking user.
func (r *Runner) userSysProcAttr(elevate bool) *syscall.SysProcAttr {
	if elevate || r.UserCredential == nil || r.euid() != 0 {
		return nil
	}
	return &syscall.SysProcAttr{Credential: r.UserCredential}
}

// childEnv returns the environment for a command. Per-user commands get the
// invoking user's HOME/XDG overlaid so they never read root's.
func (r *Runner) childEnv(elevate bool) []string {
	if elevate || len(r.UserEnv) == 0 {
		return r.Env
	}
	base := r.Env
	if base == nil {
		base = os.Environ()
	}
	return overlayEnv(base, r.UserEnv)
}

// overlayEnv returns base with each KEY=VALUE in overlay replacing the existing
// entry for KEY (or appended when absent).
func overlayEnv(base, overlay []string) []string {
	index := make(map[string]int, len(base))
	out := append([]string(nil), base...)
	for i, kv := range out {
		if k, _, ok := strings.Cut(kv, "="); ok {
			index[k] = i
		}
	}
	for _, kv := range overlay {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if i, exists := index[k]; exists {
			out[i] = kv
		} else {
			out = append(out, kv)
			index[k] = len(out) - 1
		}
	}
	return out
}

// Plan returns the exact argv that Run would execute, including the sudo prefix
// when elevate is set. It is side-effect free and used to render previews.
func Plan(argv []string, elevate bool) []string {
	if len(argv) == 0 {
		return nil
	}
	if !elevate {
		return append([]string(nil), argv...)
	}
	out := make([]string, 0, len(argv)+2)
	out = append(out, "sudo", "--")
	out = append(out, argv...)
	return out
}

// Run executes argv verbatim under a bounded timeout. When dryRun is set no
// process is started. A timeout returns a Result with TimedOut=true and an
// error wrapping context.DeadlineExceeded.
func (r *Runner) Run(ctx context.Context, argv []string, timeout time.Duration, elevate bool) (*Result, error) {
	if len(argv) == 0 {
		return nil, errors.New("owner: empty argv")
	}
	finalArgv := Plan(argv, elevate)
	res := &Result{Argv: finalArgv, DryRun: r.DryRun}
	if r.DryRun {
		return res, nil
	}
	if timeout <= 0 {
		timeout = TimeoutOwnerClean
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Resolve the binary to an absolute path before elevating, so sudo runs a
	// known binary rather than resolving through its own secure_path
	// (spec §7.6). Per-user commands additionally drop to the invoking user
	// via SysProcAttr.Credential below.
	execArgv := append([]string(nil), argv...)
	resolved, err := resolveExecutable(execArgv[0], r.Env)
	if err != nil {
		return res, err
	}
	execArgv[0] = resolved
	if elevate {
		execArgv = append([]string{"sudo", "--"}, execArgv...)
	}
	res.Argv = execArgv

	cmd := exec.CommandContext(runCtx, execArgv[0], execArgv[1:]...)
	cmd.Env = r.childEnv(elevate)
	cmd.Dir = r.Dir
	cmd.SysProcAttr = r.userSysProcAttr(elevate)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	res.Duration = time.Since(start)
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	if r.Log != nil {
		r.Log(fmt.Sprintf("run %v (%s)", finalArgv, res.Duration))
	}

	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		return res, fmt.Errorf("owner: %v timed out after %s: %w", finalArgv, timeout, context.DeadlineExceeded)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		return res, err
	}
	res.ExitCode = 0
	return res, nil
}

// Probe runs a read-only command and returns its stdout. Probes are never
// elevated and never dry-run: measurement and detection must see real output
// even when the run itself is a dry-run.
func (r *Runner) Probe(ctx context.Context, argv []string, timeout time.Duration) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("owner: empty argv")
	}
	probe := *r
	probe.DryRun = false
	res, err := probe.Run(ctx, argv, timeout, false)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// resolveExecutable returns the absolute path of name, searching env's PATH
// when set and otherwise the process PATH. It refuses a path that is not an
// executable regular file.
func resolveExecutable(name string, env []string) (string, error) {
	if name == "" {
		return "", errors.New("owner: empty executable")
	}
	if filepath.IsAbs(name) {
		if err := executableFile(name); err != nil {
			return "", err
		}
		return name, nil
	}
	pathEnv := os.Getenv("PATH")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			pathEnv = v
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		if err := executableFile(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("owner: executable %q not found in PATH", name)
}

// Available reports whether name resolves to an executable on the current
// PATH. It is used by discovery to skip an item whose owner command is absent.
func Available(name string) bool {
	_, err := resolveExecutable(name, nil)
	return err == nil
}

func executableFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return fmt.Errorf("owner: %s is not executable", path)
	}
	return nil
}

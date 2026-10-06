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
	"os/exec"
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
}

// New returns a Runner.
func New(dryRun bool) *Runner { return &Runner{DryRun: dryRun} }

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

	cmd := exec.CommandContext(runCtx, finalArgv[0], finalArgv[1:]...)
	cmd.Env = r.Env
	cmd.Dir = r.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
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

package owner

import (
	"context"
	"errors"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPlanExactArgv(t *testing.T) {
	argv := []string{"apt-get", "clean"}
	if got := Plan(argv, false); strings.Join(got, " ") != "apt-get clean" {
		t.Fatalf("got %v", got)
	}
	got := Plan(argv, true)
	want := []string{"sudo", "--", "apt-get", "clean"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", got, want)
	}
	// The input slice must not be mutated.
	if argv[0] != "apt-get" || len(argv) != 2 {
		t.Fatalf("input argv mutated: %v", argv)
	}
}

func TestDryRunDoesNotExecute(t *testing.T) {
	r := New(true)
	res, err := r.Run(context.Background(), []string{"definitely-not-a-real-binary"}, time.Second, false)
	if err != nil {
		t.Fatalf("dry run must not execute: %v", err)
	}
	if !res.DryRun || res.Argv[0] != "definitely-not-a-real-binary" {
		t.Fatalf("unexpected dry-run result: %+v", res)
	}
}

func TestRunExecutesVerbatimWithoutShell(t *testing.T) {
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("echo not available")
	}
	// An argument full of shell metacharacters must be passed literally.
	dangerous := "a; rm -rf / #"
	r := New(false)
	res, err := r.Run(context.Background(), []string{echo, dangerous}, 5*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != dangerous {
		t.Fatalf("argument was interpreted by a shell: %q", res.Stdout)
	}
}

func TestRunTimeout(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available")
	}
	r := New(false)
	res, err := r.Run(context.Background(), []string{sleep, "30"}, 50*time.Millisecond, false)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut, got %+v", res)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestRunEmptyArgv(t *testing.T) {
	if _, err := New(false).Run(context.Background(), nil, time.Second, false); err == nil {
		t.Fatal("expected error for empty argv")
	}
}

func TestCheckRoot(t *testing.T) {
	if err := CheckRoot(func() int { return 0 }, func(string) string { return "" }); err == nil {
		t.Fatal("expected refusal for root without SUDO_USER")
	}
	if err := CheckRoot(func() int { return 0 }, func(k string) string {
		if k == "SUDO_USER" {
			return "alice"
		}
		return ""
	}); err != nil {
		t.Fatalf("expected root with SUDO_USER to pass, got %v", err)
	}
	if err := CheckRoot(func() int { return 1000 }, func(string) string { return "" }); err != nil {
		t.Fatalf("expected non-root to pass, got %v", err)
	}
}

func TestUserDirsIgnoresRootXDGWhenElevated(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "XDG_CACHE_HOME":
			return "/root/.cache"
		default:
			return ""
		}
	}
	u := &user.User{Uid: "1000", Gid: "1000", Username: "alice", HomeDir: "/home/alice"}
	dirs := UserDirs(u, true, getenv)
	if strings.HasPrefix(dirs.Cache, "/root") {
		t.Fatalf("elevated resolution trusted root's XDG: %q", dirs.Cache)
	}
	if !strings.HasSuffix(dirs.Cache, ".cache") {
		t.Fatalf("cache dir = %q", dirs.Cache)
	}
}

// TestPerUserCommandDropsRootCredential proves that when the
// process is elevated, a per-user command is given the invoking user's
// credential, while an elevated command is not.
func TestPerUserCommandDropsRootCredential(t *testing.T) {
	r := New(false)
	r.Geteuid = func() int { return 0 }
	r.UserCredential = &syscall.Credential{Uid: 1000, Gid: 1000}

	attr := r.userSysProcAttr(false)
	if attr == nil || attr.Credential == nil {
		t.Fatal("per-user command must drop to the invoking user when elevated")
	}
	if attr.Credential.Uid != 1000 || attr.Credential.Gid != 1000 {
		t.Fatalf("credential = %+v", attr.Credential)
	}
	if r.userSysProcAttr(true) != nil {
		t.Fatal("elevated command must keep root credentials")
	}
}

func TestPerUserCommandCredentialSkippedWhenNotElevated(t *testing.T) {
	r := New(false)
	r.Geteuid = func() int { return 1000 }
	r.UserCredential = &syscall.Credential{Uid: 1000, Gid: 1000}
	if r.userSysProcAttr(false) != nil {
		t.Fatal("must not set credentials when not elevated")
	}
}

func TestPerUserCommandEnvOverlay(t *testing.T) {
	r := New(false)
	r.Env = []string{"PATH=/usr/bin", "HOME=/root", "TERM=xterm"}
	r.UserEnv = []string{"HOME=/home/alice", "XDG_CACHE_HOME=/home/alice/.cache"}

	env := r.childEnv(false)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["HOME"] != "/home/alice" || got["XDG_CACHE_HOME"] != "/home/alice/.cache" {
		t.Fatalf("per-user env not overlaid: %v", env)
	}
	if got["PATH"] != "/usr/bin" || got["TERM"] != "xterm" {
		t.Fatalf("inherited env lost: %v", env)
	}
	if elev := r.childEnv(true); len(elev) != 3 || elev[1] != "HOME=/root" {
		t.Fatalf("elevated command must keep the inherited env: %v", elev)
	}
}

// TestPerUserCommandEnvAppliedAtExecution proves the overlay reaches a real
// child process, so owner commands see the invoking user's HOME, not root's.
func TestPerUserCommandEnvAppliedAtExecution(t *testing.T) {
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("env not available")
	}
	r := New(false)
	r.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root"}
	r.UserEnv = []string{"HOME=/home/alice"}
	res, err := r.Run(context.Background(), []string{envBin}, 5*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "HOME=/home/alice") {
		t.Fatalf("per-user command did not see the invoking user's HOME:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "HOME=/root") {
		t.Fatalf("per-user command saw root's HOME:\n%s", res.Stdout)
	}
}

func TestCredentialAndEnvironmentHelpers(t *testing.T) {
	u := &user.User{Uid: "1000", Gid: "1000", Username: "alice", HomeDir: "/home/alice"}
	cred := Credential(u)
	if cred == nil || cred.Uid != 1000 || cred.Gid != 1000 {
		t.Fatalf("credential = %+v", cred)
	}
	if Credential(&user.User{Uid: "not-a-uid"}) != nil {
		t.Fatal("non-numeric uid must yield no credential")
	}
	dirs := Dirs{Home: "/home/alice", Cache: "/home/alice/.cache", Config: "/home/alice/.config", Data: "/home/alice/.local/share", State: "/home/alice/.local/state", Runtime: "/run/user/1000"}
	env := strings.Join(Environment(dirs), "\n")
	for _, want := range []string{"HOME=/home/alice", "XDG_CACHE_HOME=/home/alice/.cache", "XDG_RUNTIME_DIR=/run/user/1000"} {
		if !strings.Contains(env, want) {
			t.Fatalf("Environment missing %q:\n%s", want, env)
		}
	}
}

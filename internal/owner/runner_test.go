package owner

import (
	"context"
	"errors"
	"os/exec"
	"os/user"
	"strings"
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

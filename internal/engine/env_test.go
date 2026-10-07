package engine_test

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
)

// TestResolveElevatedConfiguresPerUserRunner proves that an elevated invocation
// (euid 0 with SUDO_USER set) configures the owner runner to drop to the
// invoking user for per-user commands and to pass that user's HOME/XDG, so
// owner commands never run as root (spec §7.6).
func TestResolveElevatedConfiguresPerUserRunner(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skip("cannot resolve current user")
	}
	getenv := func(k string) string {
		if k == "SUDO_USER" {
			return u.Username
		}
		return ""
	}
	env, err := engine.Resolve(engine.ResolveOptions{
		Getenv:     getenv,
		Geteuid:    func() int { return 0 },
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		Distro:     &distro.Info{ID: "ubuntu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Run.UserCredential == nil {
		t.Fatal("elevated resolve must configure a per-user credential")
	}
	if got := int(env.Run.UserCredential.Uid); got != os.Getuid() {
		t.Fatalf("credential uid = %d, want %d", got, os.Getuid())
	}
	foundHome := false
	for _, kv := range env.Run.UserEnv {
		if kv == "HOME="+u.HomeDir {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatalf("UserEnv missing the invoking user's HOME: %v", env.Run.UserEnv)
	}
}

// TestResolveNotElevatedLeavesCredentialUnset proves a normal invocation does
// not attempt to change credentials (which would fail).
func TestResolveNotElevatedLeavesCredentialUnset(t *testing.T) {
	env, err := engine.Resolve(engine.ResolveOptions{
		Getenv:     func(string) string { return "" },
		Geteuid:    func() int { return 1000 },
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		Distro:     &distro.Info{ID: "ubuntu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Run.UserCredential != nil {
		t.Fatalf("non-elevated resolve must not set a credential: %+v", env.Run.UserCredential)
	}
}

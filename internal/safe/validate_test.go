package safe_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/safe"
)

func TestValidateForDeletionRejections(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", "var/tmp/x"},
		{"double slash", "/var//tmp/x"},
		{"dotdot", "/var/tmp/../x"},
		{"dot segment", "/var/./tmp/x"},
		{"control char", "/var/tmp/x\x00y"},
		{"trailing slash", "/var/tmp/x/"},
		{"root", "/"},
		{"boot", "/boot"},
		{"usr", "/usr"},
		{"etc", "/etc"},
		{"var", "/var"},
		{"varlib", "/var/lib"},
		{"varlog", "/var/log"},
		{"home", "/home"},
		{"lib", "/lib"},
		{"lib64", "/lib64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := safe.ValidateForDeletion(tc.path)
			if err == nil {
				t.Fatalf("expected rejection for %q", tc.path)
			}
			if !errors.Is(err, safe.ErrUnsafe) {
				t.Fatalf("expected ErrUnsafe for %q, got %v", tc.path, err)
			}
		})
	}
}

func TestValidateForDeletionAcceptsUserPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cache-entry")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := safe.ValidateForDeletion(file); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateConfigProtected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "keepme")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := safe.NewValidator(-1, dir)
	if err := v.Validate(target); err == nil {
		t.Fatal("expected config-protected path to be rejected")
	}
}

func TestValidateOwnership(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owned")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := &safe.Validator{UID: os.Getuid(), CheckOwnership: true}
	if err := good.Validate(file); err != nil {
		t.Fatalf("expected owner accept, got %v", err)
	}
	bad := &safe.Validator{UID: os.Getuid() + 12345, CheckOwnership: true}
	if err := bad.Validate(file); err == nil {
		t.Fatal("expected ownership rejection")
	}
}

func TestValidateSymlinkRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := safe.ValidateForDeletion(link); err == nil {
		t.Fatal("expected symlink root to be rejected")
	}
}

func TestValidateRequireRoot(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	file := filepath.Join(other, "x")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &safe.Validator{Roots: []string{allowed}, RequireRoot: true}
	if err := v.Validate(file); err == nil {
		t.Fatal("expected path outside allowed root to be rejected")
	}
	inside := filepath.Join(allowed, "y")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(inside); err != nil {
		t.Fatalf("expected inside path accepted, got %v", err)
	}
}

func TestWithinRoot(t *testing.T) {
	if !safe.WithinRoot("/a/b/c", "/a/b") {
		t.Fatal("expected within")
	}
	if safe.WithinRoot("/a/bc", "/a/b") {
		t.Fatal("expected not within (prefix boundary)")
	}
	if !safe.WithinRoot("/a/b", "/a/b") {
		t.Fatal("expected root itself within")
	}
}

func FuzzValidate(f *testing.F) {
	seeds := []string{
		"..", "//", "/", "/var", "/etc/passwd", "\x00", "/var/tmp/../x",
		"/home/user/.cache", "/lib64", "relative", "/var//tmp", "/var/./x",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	root := os.TempDir()
	v := &safe.Validator{Roots: []string{root}, RequireRoot: true}
	f.Fuzz(func(t *testing.T, path string) {
		if err := v.Validate(path); err != nil {
			return
		}
		if !safe.WithinAnyRoot(path, v.Roots) {
			t.Fatalf("validator accepted path outside allowed roots: %q", path)
		}
	})
}

package distro

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func detectFixture(t *testing.T, name string) Info {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := DetectFrom(f)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestDetectFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		id      string
		family  string
	}{
		{"fedora", "fedora", "dnf"},
		{"ubuntu", "ubuntu", "apt"},
		{"arch", "arch", "pacman"},
		{"opensuse", "opensuse-leap", "zypper"},
		{"alpine", "alpine", "apk"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			info := detectFixture(t, tc.fixture)
			if info.ID != tc.id {
				t.Fatalf("id = %q want %q", info.ID, tc.id)
			}
			if info.Family() != tc.family {
				t.Fatalf("family = %q want %q", info.Family(), tc.family)
			}
			adapter := For(info)
			if adapter.ID() != tc.family {
				t.Fatalf("adapter = %q want %q", adapter.ID(), tc.family)
			}
		})
	}
}

func TestForUnknown(t *testing.T) {
	adapter := For(Info{ID: "gentoo"})
	if adapter.ID() != "unknown" {
		t.Fatalf("expected unknown adapter, got %q", adapter.ID())
	}
	if len(adapter.LockGuard().LockFiles) != 0 {
		t.Fatal("unknown adapter must not claim lock files")
	}
	if !reflect.DeepEqual(adapter.System(), SystemSpec{}) {
		t.Fatal("unknown adapter must return an empty system spec")
	}
}

func TestDetectCurrentSystem(t *testing.T) {
	info, err := Detect()
	if err != nil {
		t.Skipf("no /etc/os-release: %v", err)
	}
	if info.Family() == "" {
		t.Fatal("family must not be empty")
	}
}

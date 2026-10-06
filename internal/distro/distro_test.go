package distro

import (
	"os"
	"path/filepath"
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
		pm      string
	}{
		{"fedora", "fedora", "dnf", "dnf"},
		{"ubuntu", "ubuntu", "apt", "apt-get"},
		{"arch", "arch", "pacman", "pacman"},
		{"opensuse", "opensuse-leap", "zypper", "zypper"},
		{"alpine", "alpine", "apk", "apk"},
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
			if adapter.PackageManager() != tc.pm {
				t.Fatalf("pm = %q want %q", adapter.PackageManager(), tc.pm)
			}
		})
	}
}

func TestForUnknown(t *testing.T) {
	adapter := For(Info{ID: "gentoo"})
	if adapter.ID() != "unknown" {
		t.Fatalf("expected unknown adapter, got %q", adapter.ID())
	}
	if len(adapter.LockFiles()) != 0 {
		t.Fatal("unknown adapter must not claim lock files")
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

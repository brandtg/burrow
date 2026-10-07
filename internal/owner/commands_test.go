package owner

import (
	"strings"
	"testing"
)

func TestSnapRemoveRevisionValidates(t *testing.T) {
	cmd, err := SnapRemoveRevision("core20", "2015")
	if err != nil {
		t.Fatal(err)
	}
	want := "snap remove --purge core20 --revision 2015"
	if got := strings.Join(cmd.Argv, " "); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if !cmd.NeedsRoot {
		t.Fatal("snap remove must need root")
	}

	bad := []struct{ name, rev string }{
		{"-rf", "1"},
		{"Core20", "1"},
		{"core 20", "1"},
		{"core20", "-1"},
		{"core20", ""},
		{"", "1"},
	}
	for _, b := range bad {
		if _, err := SnapRemoveRevision(b.name, b.rev); err == nil {
			t.Fatalf("expected an error for name=%q revision=%q", b.name, b.rev)
		}
	}
}

func TestContainerAndDesktopCommandTables(t *testing.T) {
	cases := []struct {
		name string
		cmd  OwnerCommand
		argv string
		root bool
	}{
		{"docker build cache", DockerBuilderPrune(), "docker builder prune -f", false},
		{"docker dangling", DockerImagePrune(), "docker image prune -f", false},
		{"docker unused", DockerImagePruneAll(), "docker image prune -a -f", false},
		{"docker volumes", DockerVolumePrune(), "docker volume prune -f", false},
		{"docker all", DockerSystemPruneAll(), "docker system prune -a --volumes -f", false},
		{"podman", PodmanSystemPrune(), "podman system prune -f", false},
		{"flatpak unused", FlatpakUninstallUnused(), "flatpak uninstall --unused -y", false},
		{"flatpak repair", FlatpakRepair(), "flatpak repair", true},
	}
	for _, tc := range cases {
		if got := strings.Join(tc.cmd.Argv, " "); got != tc.argv {
			t.Fatalf("%s argv = %q, want %q", tc.name, got, tc.argv)
		}
		if tc.cmd.NeedsRoot != tc.root {
			t.Fatalf("%s NeedsRoot = %v, want %v", tc.name, tc.cmd.NeedsRoot, tc.root)
		}
	}
}

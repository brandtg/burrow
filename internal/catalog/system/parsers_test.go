package system

import (
	"testing"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/distro"
)

func TestParseJournaldUsage(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want int64
		ok   bool
	}{
		{
			name: "mebibytes",
			out:  "Archived and active journals take up 512.0M in the file system.",
			want: 512 << 20,
			ok:   true,
		},
		{
			name: "gibibytes",
			out:  "Archived and active journals take up 1.5G in the file system.",
			want: 3 << 29,
			ok:   true,
		},
		{
			name: "kibibytes",
			out:  "Archived and active journals take up 128K in the file system.",
			want: 128 << 10,
			ok:   true,
		},
		{
			name: "bytes",
			out:  "Archived and active journals take up 512B in the file system.",
			want: 512,
			ok:   true,
		},
		{
			name: "decimal",
			out:  "Archived and active journals take up 1.2GB in the file system.",
			want: 1200 * 1000 * 1000,
			ok:   true,
		},
		{
			name: "unparseable size",
			out:  "Archived and active journals take up huge in the file system.",
			want: 0,
			ok:   false,
		},
		{
			name: "no size",
			out:  "journalctl: no journals found",
			want: 0,
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseJournaldUsage(c.out)
			if got != c.want || ok != c.ok {
				t.Fatalf("parseJournaldUsage = (%d,%v), want (%d,%v)", got, ok, c.want, c.ok)
			}
		})
	}
}

// TestParseHumanSizes covers the forms the shared parser must accept now that
// parseHumanSize and parseDockerSize are gone: journald/apt-style binary
// suffixes and docker-style decimal ones.
func TestParseHumanSizes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"512", 512},
		{"0", 0},
		{"512.0M", 512 << 20},
		{"1.5G", 3 << 29},
		{"1.5GiB", 3 << 29},
		{"1.2GB", 1200 * 1000 * 1000},
		{"800MB", 800 * 1000 * 1000},
		{"50MB", 50 * 1000 * 1000},
		{"1.2kB", 1200},
		{"0B", 0},
	}
	for _, c := range cases {
		got, err := config.ParseBytes(c.in)
		if err != nil {
			t.Fatalf("ParseBytes(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	if _, err := config.ParseBytes("N/A"); err == nil {
		t.Fatal("ParseBytes must reject docker's N/A")
	}
}

func TestParseAptFreed(t *testing.T) {
	parse := distro.For(distro.Info{ID: "ubuntu"}).System().ParseOrphans
	cases := []struct {
		name  string
		out   string
		count int
		freed int64
		known bool
	}{
		{
			name: "one package in kB",
			out: "Reading package lists...\n" +
				"Building dependency tree...\n" +
				"The following packages will be REMOVED:\n" +
				"  libpython3.10-minimal\n" +
				"0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n" +
				"After this operation, 512 kB disk space will be freed.\n",
			count: 1,
			freed: 512 << 10,
			known: true,
		},
		{
			name: "two packages in MB",
			out: "The following packages will be REMOVED:\n" +
				"  linux-image-6.1.0-13-amd64 linux-headers-6.1.0-13-amd64\n" +
				"0 upgraded, 0 newly installed, 2 to remove and 0 not upgraded.\n" +
				"After this operation, 12.5 MB disk space will be freed.\n",
			count: 2,
			freed: 13107200,
			known: true,
		},
		{
			name:  "nothing to remove",
			out:   "0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n",
			count: 0,
			freed: 0,
			known: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			count, freed, known := parse(c.out)
			if count != c.count || freed != c.freed || known != c.known {
				t.Fatalf("ParseOrphans = (%d,%d,%v), want (%d,%d,%v)", count, freed, known, c.count, c.freed, c.known)
			}
		})
	}
}

func TestParseDnfOrphans(t *testing.T) {
	parse := distro.For(distro.Info{ID: "fedora"}).System().ParseOrphans
	cases := []struct {
		name  string
		out   string
		count int
	}{
		{"two packages", "python3-six\npython3-dateutil\n", 2},
		{"blank lines", "python3-six\n\npython3-dateutil\n\n", 2},
		{"none", "\n", 0},
		{"empty", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			count, freed, known := parse(c.out)
			if count != c.count || freed != 0 || known {
				t.Fatalf("ParseOrphans = (%d,%d,%v), want (%d,0,false)", count, freed, known, c.count)
			}
		})
	}
}

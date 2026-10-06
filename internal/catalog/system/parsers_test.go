package system

import "testing"

func TestParseAptRemovals(t *testing.T) {
	out := "Reading package lists...\n0 upgraded, 0 newly installed, 3 to remove and 1 not upgraded.\n"
	if n := parseAptRemovals(out); n != 3 {
		t.Fatalf("removals = %d", n)
	}
	if n := parseAptRemovals("0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded."); n != 0 {
		t.Fatalf("removals = %d", n)
	}
}

func TestParseAptFreed(t *testing.T) {
	size, ok := parseAptFreed("After this operation, 12.5 MB disk space will be freed.")
	if !ok || size != int64(12.5*1024*1024) {
		t.Fatalf("freed = %d ok=%v", size, ok)
	}
}

func TestParseResidualConfig(t *testing.T) {
	out := "Desired=Unknown/Install/Remove/Purge/Hold\nrc  oldpkg  1.0  amd64  old config\nii  keep  2.0 amd64 installed\n"
	pkgs := parseResidualConfig(out)
	if len(pkgs) != 1 || pkgs[0] != "oldpkg" {
		t.Fatalf("residual pkgs = %v", pkgs)
	}
}

func TestParseJournaldUsage(t *testing.T) {
	size, ok := parseJournaldUsage("Archived and active journals take up 512.0M in the file system.")
	if !ok || size != 512*1024*1024 {
		t.Fatalf("journald size = %d ok=%v", size, ok)
	}
}

func TestParseKernelVersion(t *testing.T) {
	if v := kernelVersion("linux-image-6.8.0-31-generic"); v != "6.8.0-31-generic" {
		t.Fatalf("kernel version = %q", v)
	}
	if v := kernelVersion("linux-image-unsigned-6.8.0-31-generic"); v != "6.8.0-31-generic" {
		t.Fatalf("kernel version = %q", v)
	}
}

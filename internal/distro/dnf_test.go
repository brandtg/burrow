package distro

import "testing"

func TestAptSystemSpec(t *testing.T) {
	spec := For(Info{ID: "ubuntu"}).System()
	if spec.PackageCache.Argv[0] != "apt-get" || spec.PackageCache.Argv[1] != "clean" {
		t.Fatalf("apt package cache = %v", spec.PackageCache.Argv)
	}
	if spec.PackageCachePath != "/var/cache/apt/archives" {
		t.Fatalf("apt package cache path = %q", spec.PackageCachePath)
	}
	if spec.ResidualConfig == nil {
		t.Fatal("apt must implement residual-config")
	}
	if !spec.HasOldKernels("linux-image-6.8.0-31-generic\n", "6.7.0-1-generic\n") {
		t.Fatal("apt should detect an old kernel")
	}
	if spec.HasOldKernels("linux-image-6.8.0-31-generic\n", "6.8.0-31-generic\n") {
		t.Fatal("apt must not report the running kernel as old")
	}
}

func TestAptParsers(t *testing.T) {
	out := "Reading package lists...\n0 upgraded, 0 newly installed, 3 to remove and 1 not upgraded.\nAfter this operation, 12.5 MB disk space will be freed.\n"
	count, freed, known := parseAptOrphans(out)
	if count != 3 || !known || freed != int64(12.5*1024*1024) {
		t.Fatalf("parseAptOrphans = %d %d %v", count, freed, known)
	}
	if n := parseAptRemovals("0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded."); n != 0 {
		t.Fatalf("removals = %d", n)
	}
	residual := parseResidualConfig("rc  oldpkg  1.0  amd64  old config\nii  keep  2.0 amd64 installed\n")
	if len(residual) != 1 || residual[0] != "oldpkg" {
		t.Fatalf("residual pkgs = %v", residual)
	}
	if v := kernelVersion("linux-image-unsigned-6.8.0-31-generic"); v != "6.8.0-31-generic" {
		t.Fatalf("kernel version = %q", v)
	}
}

func TestDnfSystemSpec(t *testing.T) {
	spec := For(Info{ID: "fedora"}).System()
	if got := spec.PackageCache.Argv; len(got) != 3 || got[0] != "dnf" || got[1] != "clean" || got[2] != "all" {
		t.Fatalf("dnf package cache = %v", got)
	}
	if spec.PackageCachePath != "/var/cache/dnf" {
		t.Fatalf("dnf package cache path = %q", spec.PackageCachePath)
	}
	if got := spec.Orphans.Argv; len(got) != 3 || got[0] != "dnf" || got[1] != "autoremove" || got[2] != "-y" {
		t.Fatalf("dnf orphans = %v", got)
	}
	if got := spec.OrphansProbe.Argv; len(got) != 5 || got[0] != "dnf" || got[1] != "repoquery" || got[2] != "--unneeded" || got[3] != "--qf" || got[4] != "%{name}\\n" {
		t.Fatalf("dnf orphans probe = %v", got)
	}
	if got := spec.OldKernels.Argv; len(got) != 3 || got[0] != "package-cleanup" || got[1] != "--oldkernels" || got[2] != "--count=2" {
		t.Fatalf("dnf old kernels = %v", got)
	}
	if spec.ResidualConfig != nil {
		t.Fatal("dnf has no residual-config concept")
	}
	if !spec.HasOldKernels("kernel-6.8.5-301.fc40.x86_64\nkernel-6.7.4-200.fc39.x86_64\n", "6.8.5-301.fc40.x86_64") {
		t.Fatal("dnf should detect an old kernel")
	}
	if spec.HasOldKernels("kernel-6.8.5-301.fc40.x86_64\n", "6.8.5-301.fc40.x86_64") {
		t.Fatal("dnf must not report the running kernel as old")
	}
}

func TestParseDnfOrphans(t *testing.T) {
	if n, _, known := parseDnfOrphans("old-pkg\nanother-old-pkg\n\n"); n != 2 || known {
		t.Fatalf("parseDnfOrphans = %d known=%v", n, known)
	}
	if n, _, _ := parseDnfOrphans("\n"); n != 0 {
		t.Fatalf("empty dnf output must yield 0, got %d", n)
	}
}

package owner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
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

// commandRow pairs a registered command with the exact expectations it must
// keep. These argv rows are the product's blast radius: they are handed to
// sudo -- verbatim, so each row is written out literally here rather than
// derived from the table in commands.go. Editing a row in commands.go without
// updating it here fails TestOwnerCommandExactArgv, and adding a command
// without adding a row here fails TestEveryRegisteredCommandIsTested.
type commandRow struct {
	ctor string // constructor name in commands.go
	cmd  OwnerCommand
	id   string
	argv []string
	root bool // expected NeedsRoot
}

func catalogCommands() []commandRow {
	return []commandRow{
		// --- Debian/Ubuntu (apt/dpkg). ---
		{"AptPackageCache", AptPackageCache(), "system.pkg-cache", []string{"apt-get", "clean"}, true},
		{"AptOrphans", AptOrphans(), "system.orphans", []string{"apt-get", "autoremove", "--purge"}, true},
		{"AptOldKernels", AptOldKernels(), "system.old-kernels", []string{"apt-get", "autoremove", "--purge"}, true},
		{"AptOrphanProbe", AptOrphanProbe(), "probe.apt.orphans", []string{"apt-get", "-s", "autoremove"}, false},
		{"DpkgListProbe", DpkgListProbe(), "probe.dpkg.list", []string{"dpkg", "-l"}, false},
		{"DpkgKernelProbe", DpkgKernelProbe(), "probe.dpkg.kernels", []string{"dpkg-query", "-W", "-f=${Package}\n", "linux-image-*"}, false},

		// --- Fedora/RHEL (dnf/rpm). ---
		{"DnfPackageCache", DnfPackageCache(), "system.pkg-cache", []string{"dnf", "clean", "all"}, true},
		{"DnfOrphans", DnfOrphans(), "system.orphans", []string{"dnf", "autoremove", "-y"}, true},
		{"DnfOldKernels", DnfOldKernels(), "system.old-kernels", []string{"package-cleanup", "--oldkernels", "--count=2"}, true},
		{"DnfOrphansProbe", DnfOrphansProbe(), "probe.dnf.unneeded", []string{"dnf", "repoquery", "--unneeded", "--qf", "%{name}\\n"}, false},
		{"RpmKernelProbe", RpmKernelProbe(), "probe.rpm.kernels", []string{"rpm", "-q", "kernel"}, false},

		// --- systemd / machine probes. ---
		{"JournaldDiskUsageProbe", JournaldDiskUsageProbe(), "probe.journald.disk", []string{"journalctl", "--disk-usage"}, false},
		{"UnameProbe", UnameProbe(), "probe.uname", []string{"uname", "-r"}, false},

		// --- Per-user toolchains (never elevated). ---
		{"NpmCacheClean", NpmCacheClean(), "dev.npm", []string{"npm", "cache", "clean", "--force"}, false},
		{"NpmCacheProbe", NpmCacheProbe(), "probe.npm.cache", []string{"npm", "config", "get", "cache"}, false},
		{"PnpmStorePrune", PnpmStorePrune(), "dev.pnpm", []string{"pnpm", "store", "prune"}, false},
		{"PnpmStoreProbe", PnpmStoreProbe(), "probe.pnpm.store", []string{"pnpm", "store", "path"}, false},
		{"BunCacheRm", BunCacheRm(), "dev.bun", []string{"bun", "pm", "cache", "rm"}, false},
		{"BunCacheProbe", BunCacheProbe(), "probe.bun.cache", []string{"bun", "pm", "cache"}, false},
		{"GoCleanModCache", GoCleanModCache(), "dev.go-mod", []string{"go", "clean", "-modcache"}, false},
		{"GoModCacheProbe", GoModCacheProbe(), "probe.go.modcache", []string{"go", "env", "GOMODCACHE"}, false},
		{"GoCleanCache", GoCleanCache(), "dev.go-build", []string{"go", "clean", "-cache"}, false},
		{"GoBuildCacheProbe", GoBuildCacheProbe(), "probe.go.gocache", []string{"go", "env", "GOCACHE"}, false},
		{"PipCachePurge", PipCachePurge(), "dev.pip", []string{"pip3", "cache", "purge"}, false},
		{"PipCacheDirProbe", PipCacheDirProbe(), "probe.pip.cache", []string{"pip3", "cache", "dir"}, false},
		{"UvCachePrune", UvCachePrune(), "dev.uv", []string{"uv", "cache", "prune"}, false},
		{"UvCacheDirProbe", UvCacheDirProbe(), "probe.uv.cache", []string{"uv", "cache", "dir"}, false},
		{"CondaClean", CondaClean(), "dev.conda", []string{"conda", "clean", "--yes", "--index-cache", "--tarballs", "--logfiles"}, false},
		{"NixCollectGarbage", NixCollectGarbage(), "dev.nix", []string{"nix-collect-garbage", "--delete-older-than", "30d"}, false},
		{"CorepackCacheClean", CorepackCacheClean(), "dev.corepack", []string{"corepack", "cache", "clean"}, false},
		{"MiseCacheClear", MiseCacheClear(), "dev.mise", []string{"mise", "cache", "clear"}, false},
		{"MiseCachePathProbe", MiseCachePathProbe(), "probe.mise.cache", []string{"mise", "cache", "path"}, false},
		{"GhClearCache", GhClearCache(), "dev.gh-cli", []string{"gh", "config", "clear-cache"}, false},

		// --- Containers (docker/podman, never elevated in v0.1). ---
		{"DockerBuilderPrune", DockerBuilderPrune(), "containers.docker-build-cache", []string{"docker", "builder", "prune", "-f"}, false},
		{"DockerImagePrune", DockerImagePrune(), "containers.docker-dangling", []string{"docker", "image", "prune", "-f"}, false},
		{"DockerImagePruneAll", DockerImagePruneAll(), "containers.docker-unused", []string{"docker", "image", "prune", "-a", "-f"}, false},
		{"DockerVolumePrune", DockerVolumePrune(), "containers.docker-volumes", []string{"docker", "volume", "prune", "-f"}, false},
		{"DockerSystemPruneAll", DockerSystemPruneAll(), "containers.docker-all", []string{"docker", "system", "prune", "-a", "--volumes", "-f"}, false},
		{"PodmanSystemPrune", PodmanSystemPrune(), "containers.podman", []string{"podman", "system", "prune", "-f"}, false},
		{"DockerSystemDFProbe", DockerSystemDFProbe(), "probe.docker.df", []string{"docker", "system", "df"}, false},

		// --- Desktop packages (flatpak/snap). ---
		{"FlatpakUninstallUnused", FlatpakUninstallUnused(), "flatpak.unused", []string{"flatpak", "uninstall", "--unused", "-y"}, false},
		{"FlatpakRepair", FlatpakRepair(), "flatpak.repair", []string{"flatpak", "repair"}, true},
		{"FlatpakListAppsProbe", FlatpakListAppsProbe(), "probe.flatpak.apps", []string{"flatpak", "list", "--app", "--columns=application"}, false},
		{"SnapListProbe", SnapListProbe(), "probe.snap.list", []string{"snap", "list"}, false},
		{"SnapListAllProbe", SnapListAllProbe(), "probe.snap.list-all", []string{"snap", "list", "--all"}, false},
	}
}

// parameterizedCtors are the constructors whose argv carries caller-provided
// values. They are exercised in their own tests below and in
// parameterizedInstances, and count toward the coverage guard.
var parameterizedCtors = []string{
	"AptResidualConfig",
	"JournaldVacuumSize",
	"JournaldVacuumTime",
	"SystemdTmpfilesClean",
	"SnapRemoveRevision",
}

// retiredCtors names constructors that are on another track's removal list
// (the unused docker/podman info probes). They are excluded from the coverage
// guard so it stays accurate before and after that track lands; once the
// functions are gone the exclusion is vacuous.
var retiredCtors = map[string]bool{
	"DockerInfoProbe": true,
	"PodmanInfoProbe": true,
}

func TestOwnerCommandExactArgv(t *testing.T) {
	for _, r := range catalogCommands() {
		if !r.cmd.Valid() {
			t.Errorf("%s: command is not Valid()", r.ctor)
		}
		if r.cmd.ID != r.id {
			t.Errorf("%s: ID = %q, want %q", r.ctor, r.cmd.ID, r.id)
		}
		if !reflect.DeepEqual(r.cmd.Argv, r.argv) {
			t.Errorf("%s: argv = %q, want %q", r.ctor, r.cmd.Argv, r.argv)
		}
		if r.cmd.NeedsRoot != r.root {
			t.Errorf("%s: NeedsRoot = %v, want %v", r.ctor, r.cmd.NeedsRoot, r.root)
		}
	}
}

// dpkgQueryFormatArg is the single argv element that legitimately carries
// characters on the metacharacter ban list: ${Package} and a trailing newline
// are the dpkg-query format for one package name per line. The Runner execs
// argv directly (never through a shell), so $ is inert, and the element is
// pinned to these exact bytes by TestOwnerCommandExactArgv.
const dpkgQueryFormatArg = "-f=${Package}\n"

var shellMetaChars = []string{";", "|", "&", "$", "`", ">", "<", "\n", "\r"}

func TestOwnerCommandsAreConstantAndShellSafe(t *testing.T) {
	check := func(what string, argv []string) {
		for i, arg := range argv {
			if arg == "" {
				t.Errorf("%s: argv[%d] is empty", what, i)
				continue
			}
			if arg == dpkgQueryFormatArg {
				continue
			}
			for _, meta := range shellMetaChars {
				if strings.Contains(arg, meta) {
					t.Errorf("%s: argv[%d] = %q contains shell metacharacter %q", what, i, arg, meta)
				}
			}
			for _, ph := range []string{"%s", "%d", "{}", "{{", "$1"} {
				if strings.Contains(arg, ph) {
					t.Errorf("%s: argv[%d] = %q contains interpolation placeholder %q", what, i, arg, ph)
				}
			}
		}
	}
	for _, r := range catalogCommands() {
		check(r.ctor, r.cmd.Argv)
	}
	for _, p := range parameterizedInstances(t) {
		check(p.name+"(sample)", p.argv)
	}
}

// parameterizedInstances builds one representative command per parameterized
// constructor, for the sweeps that only apply to fully-built commands.
func parameterizedInstances(t *testing.T) []struct {
	name string
	argv []string
} {
	t.Helper()
	residual, err := AptResidualConfig([]string{"pkg1", "pkg2"})
	if err != nil {
		t.Fatalf("AptResidualConfig: %v", err)
	}
	snap, err := SnapRemoveRevision("core20", "2015")
	if err != nil {
		t.Fatalf("SnapRemoveRevision: %v", err)
	}
	return []struct {
		name string
		argv []string
	}{
		{"AptResidualConfig", residual.Argv},
		{"JournaldVacuumSize", JournaldVacuumSize("500M").Argv},
		{"JournaldVacuumTime", JournaldVacuumTime("7d").Argv},
		{"SystemdTmpfilesClean", SystemdTmpfilesClean("system.tmp").Argv},
		{"SnapRemoveRevision", snap.Argv},
	}
}

// mutatingTokens are argv tokens that change state on disk. Read-only probes
// must never carry one.
var mutatingTokens = map[string]bool{
	"remove": true, "rm": true, "delete": true, "clean": true,
	"autoremove": true, "prune": true, "uninstall": true,
}

func TestProbeCommandsAreNonMutating(t *testing.T) {
	// After the ReadOnly field is gone the ID prefix "probe." is the only
	// surviving discriminator between probes and mutating commands.
	seen := 0
	for _, r := range catalogCommands() {
		if !strings.HasPrefix(r.id, "probe.") {
			continue
		}
		seen++
		if r.root {
			t.Errorf("%s: probe must never be elevated", r.ctor)
		}
		for i, arg := range r.cmd.Argv {
			tok := strings.ToLower(strings.TrimLeft(arg, "-"))
			if !mutatingTokens[tok] {
				continue
			}
			// The apt-get orphan probe is the one probe whose argv names a
			// mutating verb; it is read-only only because the -s simulate
			// flag is present. Without -s this is a mutating argv.
			if r.ctor == "AptOrphanProbe" && containsString(r.cmd.Argv, "-s") {
				continue
			}
			t.Errorf("%s: probe argv[%d] = %q is a mutating token", r.ctor, i, arg)
		}
	}
	if seen == 0 {
		t.Fatal("no probe rows found; probe discriminator (probe.* ids) is gone")
	}
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// TestEveryRegisteredCommandIsTested parses commands.go and requires every
// exported constructor returning an OwnerCommand to appear in this file's
// table (or in parameterizedCtors), so a new command cannot ship untested.
func TestEveryRegisteredCommandIsTested(t *testing.T) {
	reg, err := registeredOwnerCommands("commands.go")
	if err != nil {
		t.Fatalf("parse commands.go: %v", err)
	}
	for name := range retiredCtors {
		delete(reg, name)
	}
	covered := map[string]bool{}
	for _, r := range catalogCommands() {
		covered[r.ctor] = true
	}
	for _, name := range parameterizedCtors {
		covered[name] = true
	}
	for name := range reg {
		if !covered[name] {
			t.Errorf("%s returns an OwnerCommand but has no expectations in commands_test.go", name)
		}
	}
	for name := range covered {
		if !reg[name] {
			t.Errorf("commands_test.go covers %s, which no longer returns an OwnerCommand in commands.go", name)
		}
	}
	if len(reg) != len(covered) {
		t.Errorf("registered constructors = %d, covered = %d", len(reg), len(covered))
	}
}

func registeredOwnerCommands(path string) (map[string]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		if fn.Type.Results == nil {
			continue
		}
		for _, res := range fn.Type.Results.List {
			if id, ok := res.Type.(*ast.Ident); ok && id.Name == "OwnerCommand" {
				out[fn.Name.Name] = true
			}
		}
	}
	return out, nil
}

// --- Parameterized constructors: the variable part is appended by the caller
// and validated first; it is never a placeholder baked into the table. ---

func TestAptResidualConfigAppendsValidatedNames(t *testing.T) {
	cmd, err := AptResidualConfig([]string{"libc6", "netbase2"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dpkg", "--purge", "libc6", "netbase2"}
	if !reflect.DeepEqual(cmd.Argv, want) {
		t.Fatalf("argv = %q, want %q", cmd.Argv, want)
	}
	// The registered row is the constant prefix; every element after it must
	// be a validated package name appended by the caller (invariant I4).
	if !reflect.DeepEqual(cmd.Argv[:2], []string{"dpkg", "--purge"}) {
		t.Fatalf("argv prefix = %q, want {dpkg --purge}", cmd.Argv[:2])
	}
	for _, pkg := range cmd.Argv[2:] {
		if !ValidPackageName(pkg) {
			t.Fatalf("argv carries invalid package name %q", pkg)
		}
	}
	for _, bad := range [][]string{{}, {"-evil"}, {"Evil"}, {"two words"}, {"evil;rm"}} {
		if _, err := AptResidualConfig(bad); err == nil {
			t.Fatalf("AptResidualConfig(%q) accepted an invalid list", bad)
		}
	}
	// The subset check bounds the purge list to the parsed ^rc set.
	if err := ValidatePackageSubset([]string{"libc6"}, map[string]bool{"libc6": true}); err != nil {
		t.Fatalf("ValidatePackageSubset: %v", err)
	}
	if err := ValidatePackageSubset([]string{"libc6", "rogue"}, map[string]bool{"libc6": true}); err == nil {
		t.Fatal("ValidatePackageSubset accepted a name outside the parsed set")
	}
}

func TestSnapRemoveRevisionAppendsValidatedFields(t *testing.T) {
	cmd, err := SnapRemoveRevision("core20", "2015")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"snap", "remove", "--purge", "core20", "--revision", "2015"}
	if !reflect.DeepEqual(cmd.Argv, want) {
		t.Fatalf("argv = %q, want %q", cmd.Argv, want)
	}
	if !reflect.DeepEqual(cmd.Argv[:3], []string{"snap", "remove", "--purge"}) {
		t.Fatalf("argv prefix = %q, want {snap remove --purge}", cmd.Argv[:3])
	}
	if cmd.Argv[3] != "core20" || !ValidSnapName(cmd.Argv[3]) {
		t.Fatalf("argv carries invalid snap name %q", cmd.Argv[3])
	}
	if cmd.Argv[4] != "--revision" || !ValidSnapRevision(cmd.Argv[5]) {
		t.Fatalf("argv carries invalid snap revision %q", cmd.Argv[5])
	}
}

func TestJournaldVacuumArgv(t *testing.T) {
	size := JournaldVacuumSize("500M")
	if want := []string{"journalctl", "--vacuum-size=500M"}; !reflect.DeepEqual(size.Argv, want) {
		t.Fatalf("argv = %q, want %q", size.Argv, want)
	}
	age := JournaldVacuumTime("7d")
	if want := []string{"journalctl", "--vacuum-time=7d"}; !reflect.DeepEqual(age.Argv, want) {
		t.Fatalf("argv = %q, want %q", age.Argv, want)
	}
	if size.ID != "system.journald" || age.ID != "system.journald" {
		t.Fatalf("IDs = %q / %q, want system.journald", size.ID, age.ID)
	}
	if !size.NeedsRoot || !age.NeedsRoot {
		t.Fatal("journald vacuum needs root")
	}
	// The config value is appended to the constant --vacuum-{size,time}= flag
	// as a single element; it never stands alone as its own argv entry.
	for _, cmd := range []OwnerCommand{size, age} {
		if len(cmd.Argv) != 2 || !strings.HasPrefix(cmd.Argv[1], "--vacuum-") {
			t.Fatalf("argv %q does not append the value to the --vacuum- flag", cmd.Argv)
		}
	}
}

func TestSystemdTmpfilesCleanArgv(t *testing.T) {
	cmd := SystemdTmpfilesClean("system.rotated-logs")
	if want := []string{"systemd-tmpfiles", "--clean"}; !reflect.DeepEqual(cmd.Argv, want) {
		t.Fatalf("argv = %q, want %q", cmd.Argv, want)
	}
	if cmd.ID != "system.rotated-logs" {
		t.Fatalf("ID = %q, want the caller's id", cmd.ID)
	}
	if !cmd.NeedsRoot {
		t.Fatal("systemd-tmpfiles --clean needs root")
	}
}

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/safe"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.General.Color != "auto" {
		t.Fatalf("unexpected general defaults: %+v", c.General)
	}
	if c.Clean.JournaldMaxSize != DefaultJournaldMaxSize {
		t.Fatalf("journald size = %q", c.Clean.JournaldMaxSize)
	}
	if len(c.Analyze.Ignore) == 0 || !c.Analyze.OneFileSystem {
		t.Fatalf("unexpected analyze defaults: %+v", c.Analyze)
	}
	if len(c.Downloads.InstallerExtensions) != len(DefaultInstallerExtensions) {
		t.Fatalf("downloads extensions = %v", c.Downloads.InstallerExtensions)
	}
	if c.Downloads.InstallerMinAge != DefaultInstallerMinAge || c.Downloads.OldMinAge != DefaultOldMinAge || c.Downloads.OldMinSize != DefaultOldMinSize {
		t.Fatalf("downloads defaults = %+v", c.Downloads)
	}
}

func TestLoadMissingUsesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.General.Color != "auto" {
		t.Fatalf("expected defaults for missing file")
	}
}

func TestLoadMergesOverDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[general]
color = "never"

[clean]
min_size = "10M"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.General.Color != "never" {
		t.Fatalf("color = %q", c.General.Color)
	}
	if c.Clean.MinSize != "10M" {
		t.Fatalf("min_size = %q", c.Clean.MinSize)
	}
	if c.Clean.JournaldMaxSize != DefaultJournaldMaxSize {
		t.Fatalf("unspecified field lost its default: %q", c.Clean.JournaldMaxSize)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	c := Default()
	env := map[string]string{
		"BURROW_COLOR":           "never",
		"BURROW_MIN_SIZE":        "5M",
		"BURROW_PROTECTED_PATHS": "/a,/b",
		"BURROW_PROJECT_PATHS":   "/p,/q",
	}
	c.ApplyEnv(func(k string) string { return env[k] })
	if c.General.Color != "never" || c.Clean.MinSize != "5M" {
		t.Fatalf("env override failed: %+v", c)
	}
	if len(c.Clean.ProtectedPaths) != 2 || c.Clean.ProtectedPaths[1] != "/b" {
		t.Fatalf("protected paths = %v", c.Clean.ProtectedPaths)
	}
	if len(c.Purge.Paths) != 2 || c.Purge.Paths[1] != "/q" {
		t.Fatalf("project paths = %v", c.Purge.Paths)
	}
}

func TestApplyEnvLegacyProjectPathsFallback(t *testing.T) {
	c := Default()
	c.ApplyEnv(func(k string) string {
		if k == "BURROW_PURGE_PATHS" {
			return "/legacy"
		}
		return ""
	})
	if len(c.Purge.Paths) != 1 || c.Purge.Paths[0] != "/legacy" {
		t.Fatalf("legacy project paths = %v", c.Purge.Paths)
	}
}

func TestApplyEnvProjectPathsWinOverLegacy(t *testing.T) {
	c := Default()
	env := map[string]string{
		"BURROW_PROJECT_PATHS": "/new",
		"BURROW_PURGE_PATHS":   "/legacy",
	}
	c.ApplyEnv(func(k string) string { return env[k] })
	if len(c.Purge.Paths) != 1 || c.Purge.Paths[0] != "/new" {
		t.Fatalf("project paths = %v, want [/new]", c.Purge.Paths)
	}
}

func TestLoadProjectsSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[projects]
paths = ["/srv/code"]
max_age = "14d"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWithHome(path, "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Purge.Paths) != 1 || c.Purge.Paths[0] != "/srv/code" {
		t.Fatalf("paths = %v", c.Purge.Paths)
	}
	if c.Purge.MaxAge != "14d" {
		t.Fatalf("max_age = %q", c.Purge.MaxAge)
	}
	if c.Purge.ActiveWithin != "30d" {
		t.Fatalf("unspecified active_within lost its default: %q", c.Purge.ActiveWithin)
	}
}

func TestLoadLegacyPurgeSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[purge]
paths = ["/old/code"]
max_age = "21d"
active_within = "0"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWithHome(path, "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Purge.Paths) != 1 || c.Purge.Paths[0] != "/old/code" {
		t.Fatalf("legacy paths not merged: %v", c.Purge.Paths)
	}
	if c.Purge.MaxAge != "21d" || c.Purge.ActiveWithin != "0" {
		t.Fatalf("legacy windows not merged: %+v", c.Purge)
	}
	if !c.LegacyPurge.zero() {
		t.Fatalf("legacy section must be cleared after merge: %+v", c.LegacyPurge)
	}
}

func TestLoadProjectsSectionWinsOverLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[projects]
paths = ["/new/code"]

[purge]
paths = ["/old/code"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWithHome(path, "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Purge.Paths) != 1 || c.Purge.Paths[0] != "/new/code" {
		t.Fatalf("projects section must win: %v", c.Purge.Paths)
	}
	if !c.LegacyPurge.zero() {
		t.Fatalf("legacy section must be cleared: %+v", c.LegacyPurge)
	}
}

func TestLoadExpandsHomeInPathKeys(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[clean]
protected_paths = ["~/secret"]

[projects]
paths = ["~/code"]

[analyze]
ignore = ["~/tmp"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWithHome(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Clean.ProtectedPaths[0]; got != filepath.Join(home, "secret") {
		t.Fatalf("protected path = %q, want %q", got, filepath.Join(home, "secret"))
	}
	if got := c.Purge.Paths[0]; got != filepath.Join(home, "code") {
		t.Fatalf("project path = %q, want %q", got, filepath.Join(home, "code"))
	}
	if got := c.Analyze.Ignore[0]; got != filepath.Join(home, "tmp") {
		t.Fatalf("ignore path = %q, want %q", got, filepath.Join(home, "tmp"))
	}
}

// TestProtectedPathsExpandedProtectHome pins the fail-open bug: an unexpanded
// "~/secret" protected path used to match nothing.
func TestProtectedPathsExpandedProtectHome(t *testing.T) {
	home := t.TempDir()
	secret := filepath.Join(home, "secret")
	other := filepath.Join(home, "other")
	for _, dir := range []string{secret, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[clean]
protected_paths = ["~/secret"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWithHome(path, home)
	if err != nil {
		t.Fatal(err)
	}
	v := safe.NewValidator(-1, c.Clean.ProtectedPaths...)
	if err := v.Validate(secret); err == nil {
		t.Fatalf("expanded protected path must protect %s", secret)
	}
	if err := v.Validate(other); err != nil {
		t.Fatalf("sibling path must remain valid: %v", err)
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{
		"0":    0,
		"10M":  10 << 20,
		"1G":   1 << 30,
		"512K": 512 << 10,
		"1.5M": 1572864,
		"1234": 1234,
		"2GiB": 2 << 30,
	}
	for in, want := range cases {
		got, err := ParseBytes(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Fatalf("%s: got %d want %d", in, got, want)
		}
	}
	if _, err := ParseBytes("bogus"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseDuration(t *testing.T) {
	d, err := ParseDuration("7d")
	if err != nil {
		t.Fatal(err)
	}
	if d != 7*24*time.Hour {
		t.Fatalf("7d = %v", d)
	}
	d, err = ParseDuration("90s")
	if err != nil || d != 90*time.Second {
		t.Fatalf("90s = %v err=%v", d, err)
	}
	if _, err := ParseDuration("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExpandHome(t *testing.T) {
	if got := ExpandHome("~/Projects", "/home/u"); got != "/home/u/Projects" {
		t.Fatalf("got %q", got)
	}
	if got := ExpandHome("/abs", "/home/u"); got != "/abs" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePaths(t *testing.T) {
	p := ResolvePaths("/cfg", "/data", "/state", "/cache")
	if p.ConfigFile != "/cfg/burrow/config.toml" {
		t.Fatalf("config = %q", p.ConfigFile)
	}
	if p.Journal != "/state/burrow/operations.jsonl" {
		t.Fatalf("journal = %q", p.Journal)
	}
	if p.TrashDir != "/data/Trash" {
		t.Fatalf("trash = %q", p.TrashDir)
	}
}

func TestCuratedDefaults(t *testing.T) {
	c := Default()
	if c.Dev.MaxAge != "30d" || c.Dev.Preselect {
		t.Fatalf("dev defaults = %+v, want max_age 30d and preselect off", c.Dev)
	}
	if c.UserCache.SweepUnknown || c.UserCache.MaxAge != "30d" {
		t.Fatalf("user-cache defaults = %+v", c.UserCache)
	}
	if c.Purge.ActiveWithin != "30d" || c.Purge.MaxAge != "7d" {
		t.Fatalf("purge defaults = %+v, want active_within 30d and max_age 7d", c.Purge)
	}
}

func TestApplyEnvCuratedOverrides(t *testing.T) {
	c := Default()
	env := map[string]string{
		"BURROW_DEV_MAX_AGE":              "14d",
		"BURROW_DEV_PRESELECT":            "true",
		"BURROW_USER_CACHE_SWEEP_UNKNOWN": "true",
		"BURROW_USER_CACHE_MAX_AGE":       "7d",
		"BURROW_PURGE_ACTIVE_WITHIN":      "0",
	}
	c.ApplyEnv(func(k string) string { return env[k] })
	if c.Dev.MaxAge != "14d" || !c.Dev.Preselect || !c.UserCache.SweepUnknown || c.UserCache.MaxAge != "7d" {
		t.Fatalf("curated env overrides failed: %+v %+v", c.Dev, c.UserCache)
	}
	if c.Purge.ActiveWithin != "0" {
		t.Fatalf("active_within = %q", c.Purge.ActiveWithin)
	}
}

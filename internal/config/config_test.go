package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.General.Color != "auto" || c.General.LogLevel != "info" {
		t.Fatalf("unexpected general defaults: %+v", c.General)
	}
	if c.Clean.JournaldMaxSize != "500M" {
		t.Fatalf("journald size = %q", c.Clean.JournaldMaxSize)
	}
	if c.Trash.Backend != "native" {
		t.Fatalf("trash backend = %q", c.Trash.Backend)
	}
	if len(c.Analyze.Ignore) == 0 || c.Analyze.OneFileSystem {
		t.Fatalf("unexpected analyze defaults: %+v", c.Analyze)
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
log_level = "debug"

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
	if c.General.LogLevel != "debug" {
		t.Fatalf("log_level = %q", c.General.LogLevel)
	}
	if c.General.Color != "auto" {
		t.Fatalf("unspecified field lost its default: %q", c.General.Color)
	}
	if c.Clean.MinSize != "10M" {
		t.Fatalf("min_size = %q", c.Clean.MinSize)
	}
	if c.Trash.Backend != "native" {
		t.Fatalf("trash default lost: %q", c.Trash.Backend)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	c := Default()
	env := map[string]string{
		"BURROW_COLOR":           "never",
		"BURROW_MIN_SIZE":        "5M",
		"BURROW_PROTECTED_PATHS": "/a,/b",
		"BURROW_TRASH_BACKEND":   "external",
	}
	c.ApplyEnv(func(k string) string { return env[k] })
	if c.General.Color != "never" || c.Clean.MinSize != "5M" {
		t.Fatalf("env override failed: %+v", c)
	}
	if len(c.Clean.ProtectedPaths) != 2 || c.Clean.ProtectedPaths[1] != "/b" {
		t.Fatalf("protected paths = %v", c.Clean.ProtectedPaths)
	}
	if c.Trash.Backend != "external" {
		t.Fatalf("backend = %q", c.Trash.Backend)
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
	if c.Dev.MaxAge != "30d" {
		t.Fatalf("dev max_age = %q", c.Dev.MaxAge)
	}
	if c.UserCache.SweepUnknown || c.UserCache.MaxAge != "30d" {
		t.Fatalf("user-cache defaults = %+v", c.UserCache)
	}
	if c.Purge.ActiveWithin != "30d" {
		t.Fatalf("purge active_within = %q", c.Purge.ActiveWithin)
	}
	if c.Purge.MaxAgeByType["node_modules"] != "30d" || c.Purge.MaxAgeByType["__pycache__"] != "0" {
		t.Fatalf("purge max_age_by_type = %v", c.Purge.MaxAgeByType)
	}
}

func TestApplyEnvCuratedOverrides(t *testing.T) {
	c := Default()
	env := map[string]string{
		"BURROW_DEV_MAX_AGE":              "14d",
		"BURROW_USER_CACHE_SWEEP_UNKNOWN": "true",
		"BURROW_USER_CACHE_MAX_AGE":       "7d",
		"BURROW_PURGE_ACTIVE_WITHIN":      "0",
	}
	c.ApplyEnv(func(k string) string { return env[k] })
	if c.Dev.MaxAge != "14d" || !c.UserCache.SweepUnknown || c.UserCache.MaxAge != "7d" {
		t.Fatalf("curated env overrides failed: %+v %+v", c.Dev, c.UserCache)
	}
	if c.Purge.ActiveWithin != "0" {
		t.Fatalf("active_within = %q", c.Purge.ActiveWithin)
	}
}

func TestLoadPurgeMaxAgeByType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[purge]
active_within = "10d"
[purge.max_age_by_type]
node_modules = "60d"
target = "3d"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Purge.ActiveWithin != "10d" {
		t.Fatalf("active_within = %q", c.Purge.ActiveWithin)
	}
	if c.Purge.MaxAgeByType["node_modules"] != "60d" || c.Purge.MaxAgeByType["target"] != "3d" {
		t.Fatalf("max_age_by_type = %v", c.Purge.MaxAgeByType)
	}
}

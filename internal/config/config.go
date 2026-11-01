// Package config loads burrow's TOML configuration, applies the documented
// precedence (CLI flag > env > file > default), and resolves the effective
// paths.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Defaults shared by the config schema and the catalog packages. Keeping one
// definition here means `burrow config` and the categories cannot drift.
const (
	DefaultJournaldMaxSize = "500M"
	DefaultInstallerMinAge = "30d"
	DefaultOldMinAge       = "90d"
	DefaultOldMinSize      = "100M"
)

var (
	DefaultInstallerExtensions = []string{".deb", ".rpm", ".AppImage", ".iso", ".pkg.tar.zst", ".snap", ".flatpakref"}
	DefaultAnalyzeIgnore       = []string{"/proc", "/dev", "/sys"}
)

// Config is the full config.toml schema.
type Config struct {
	General General `toml:"general"`
	Clean   Clean   `toml:"clean"`
	// Purge is the `[projects]` section. The field name stays Purge so callers
	// keep compiling.
	Purge Purge `toml:"projects"`
	// LegacyPurge is the deprecated `[purge]` section. It is decoded only for
	// compatibility, merged into Purge when `[projects]` is absent, and then
	// cleared so it is never written back. Do not read it outside Load.
	LegacyPurge Purge     `toml:"purge,omitempty" json:"-"`
	Analyze     Analyze   `toml:"analyze"`
	Downloads   Downloads `toml:"downloads"`
	Dev         Dev       `toml:"dev"`
	UserCache   UserCache `toml:"user-cache"`
}

// General holds top-level UI settings.
type General struct {
	Color string `toml:"color"`
}

// Clean holds cleaner defaults.
type Clean struct {
	DefaultCategories []string `toml:"default_categories"`
	JournaldMaxSize   string   `toml:"journald_max_size"`
	JournaldMaxAge    string   `toml:"journald_max_age"`
	MinSize           string   `toml:"min_size"`
	ProtectedPaths    []string `toml:"protected_paths"`
}

// Purge holds project-artifact settings. With Trash recovery a wrong guess
// costs disk space, not data, so the policy is deliberately small: one root
// list, one inactivity window, one active-project window.
type Purge struct {
	Paths []string `toml:"paths"`
	// MaxAge is the inactivity window. Artifacts with activity newer than this
	// are kept. Zero disables the age filter.
	MaxAge string `toml:"max_age"`
	// ActiveWithin keeps artifacts belonging to a project whose git repository
	// saw a commit within this window, regardless of artifact age. Zero disables
	// the active-project guard.
	ActiveWithin string `toml:"active_within"`
}

// Dev holds developer-cache settings.
type Dev struct {
	// MaxAge gates pre-selection of a dev cache touched within the window. Zero
	// disables idle gating.
	MaxAge string `toml:"max_age"`
	// Preselect opts dev caches into pre-selection. Off by default: the owner
	// of the machine actively uses these caches, so rows are shown but stay
	// unselected unless opted in per run or here.
	Preselect bool `toml:"preselect"`
}

// UserCache holds the generic XDG cache sweep settings.
type UserCache struct {
	// SweepUnknown pre-selects app caches that are neither in the curated cheap
	// allowlist nor known-expensive. Off by default (curated).
	SweepUnknown bool `toml:"sweep_unknown"`
	// MaxAge gates pre-selection of an app cache touched within the window.
	MaxAge string `toml:"max_age"`
}

// Analyze holds scan settings.
type Analyze struct {
	OneFileSystem bool     `toml:"one_file_system"`
	Ignore        []string `toml:"ignore"`
}

// Downloads holds installer-sweep settings.
type Downloads struct {
	InstallerExtensions []string `toml:"installer_extensions"`
	InstallerMinAge     string   `toml:"installer_min_age"`
	// OldMinAge and OldMinSize bound the `downloads.old` sweep (default OFF).
	OldMinAge  string `toml:"old_min_age"`
	OldMinSize string `toml:"old_min_size"`
}

// Default returns the built-in defaults.
func Default() *Config {
	return &Config{
		General: General{Color: "auto"},
		Clean: Clean{
			DefaultCategories: []string{"projects", "dev", "user-cache", "desktop-pkgs", "downloads"},
			JournaldMaxSize:   DefaultJournaldMaxSize,
			JournaldMaxAge:    "",
			MinSize:           "0",
			ProtectedPaths:    []string{"/etc", "/usr", "/var/lib"},
		},
		Purge: Purge{
			Paths:        []string{"~/Projects", "~/src", "~/dev", "~/code", "~/work"},
			MaxAge:       "7d",
			ActiveWithin: "30d",
		},
		Analyze: Analyze{
			OneFileSystem: true,
			Ignore:        append([]string(nil), DefaultAnalyzeIgnore...),
		},
		Downloads: Downloads{
			InstallerExtensions: append([]string(nil), DefaultInstallerExtensions...),
			InstallerMinAge:     DefaultInstallerMinAge,
			OldMinAge:           DefaultOldMinAge,
			OldMinSize:          DefaultOldMinSize,
		},
		Dev:       Dev{MaxAge: "30d", Preselect: false},
		UserCache: UserCache{SweepUnknown: false, MaxAge: "30d"},
	}
}

// Load reads path into a copy of the defaults, expanding a leading "~" in
// every path-valued key against the invoking user's home. A missing file is not
// an error.
func Load(path string) (*Config, error) {
	return LoadWithHome(path, invokingHome())
}

// LoadWithHome is Load with an explicit home for "~" expansion. Callers that
// already resolved the invoking user's home can use it directly.
func LoadWithHome(path, home string) (*Config, error) {
	cfg := Default()
	if path == "" {
		cfg.expandPaths(home)
		return cfg, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			cfg.expandPaths(home)
			return cfg, nil
		}
		return nil, err
	}
	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	cfg.applyLegacyPurge(md)
	cfg.expandPaths(home)
	return cfg, nil
}

// applyLegacyPurge merges the deprecated [purge] section into [projects].
// [projects] wins when both are present; the legacy field is cleared either way
// so it is never encoded back out.
func (c *Config) applyLegacyPurge(md toml.MetaData) {
	if md.IsDefined("projects") {
		c.LegacyPurge = Purge{}
		return
	}
	if c.LegacyPurge.zero() {
		return
	}
	if len(c.LegacyPurge.Paths) > 0 {
		c.Purge.Paths = c.LegacyPurge.Paths
	}
	if c.LegacyPurge.MaxAge != "" {
		c.Purge.MaxAge = c.LegacyPurge.MaxAge
	}
	if c.LegacyPurge.ActiveWithin != "" {
		c.Purge.ActiveWithin = c.LegacyPurge.ActiveWithin
	}
	c.LegacyPurge = Purge{}
}

func (p Purge) zero() bool {
	return len(p.Paths) == 0 && p.MaxAge == "" && p.ActiveWithin == ""
}

// expandPaths applies "~" expansion to every path-valued key. A config path
// that is left unexpanded silently matches nothing, which is a fail-open bug
// for protected_paths.
func (c *Config) expandPaths(home string) {
	if home == "" {
		return
	}
	c.Clean.ProtectedPaths = expandHomeList(c.Clean.ProtectedPaths, home)
	c.Purge.Paths = expandHomeList(c.Purge.Paths, home)
	c.Analyze.Ignore = expandHomeList(c.Analyze.Ignore, home)
}

func expandHomeList(paths []string, home string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ExpandHome(strings.TrimSpace(p), home)
	}
	return out
}

// invokingHome resolves the invoking user's home the same way owner.UserDirs
// does: SUDO_USER's passwd entry when elevated, otherwise the current user's.
func invokingHome() string {
	if sudo := os.Getenv("SUDO_USER"); sudo != "" {
		if u, err := user.Lookup(sudo); err == nil && u.HomeDir != "" {
			return u.HomeDir
		}
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// ApplyEnv overlays BURROW_* environment variables (env precedence sits between
// CLI flags and the config file). getenv may be nil to use os.Getenv.
func (c *Config) ApplyEnv(getenv func(string) string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if v := getenv("BURROW_COLOR"); v != "" {
		c.General.Color = v
	}
	if v := getenv("BURROW_MIN_SIZE"); v != "" {
		c.Clean.MinSize = v
	}
	if v := getenv("BURROW_PROTECTED_PATHS"); v != "" {
		c.Clean.ProtectedPaths = splitList(v)
	}
	if v := getenv("BURROW_PURGE_MAX_AGE"); v != "" {
		c.Purge.MaxAge = v
	}
	if v := getenv("BURROW_PROJECT_PATHS"); v != "" {
		c.Purge.Paths = splitList(v)
	} else if v := getenv("BURROW_PURGE_PATHS"); v != "" {
		// Deprecated compat for the pre-[projects] variable name.
		c.Purge.Paths = splitList(v)
	}
	if v := getenv("BURROW_PURGE_ACTIVE_WITHIN"); v != "" {
		c.Purge.ActiveWithin = v
	}
	if v := getenv("BURROW_DEV_MAX_AGE"); v != "" {
		c.Dev.MaxAge = v
	}
	if v := getenv("BURROW_DEV_PRESELECT"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Dev.Preselect = b
		}
	}
	if v := getenv("BURROW_USER_CACHE_MAX_AGE"); v != "" {
		c.UserCache.MaxAge = v
	}
	if v := getenv("BURROW_USER_CACHE_SWEEP_UNKNOWN"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.UserCache.SweepUnknown = b
		}
	}
	if v := getenv("BURROW_ANALYZE_ONE_FILE_SYSTEM"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Analyze.OneFileSystem = b
		}
	}
	c.expandPaths(invokingHome())
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Paths are the resolved on-disk locations burrow uses.
type Paths struct {
	ConfigFile string
	Whitelist  string
	Journal    string
	CacheDir   string
	StateDir   string
	DataDir    string
	TrashDir   string
}

// ResolvePaths derives the standard XDG locations for the invoking user. The
// xdg* arguments are the already-resolved XDG directories (from engine.Env).
func ResolvePaths(xdgConfig, xdgData, xdgState, xdgCache string) Paths {
	return Paths{
		ConfigFile: filepath.Join(xdgConfig, "burrow", "config.toml"),
		Whitelist:  filepath.Join(xdgConfig, "burrow", "whitelist"),
		Journal:    filepath.Join(xdgState, "burrow", "operations.jsonl"),
		CacheDir:   filepath.Join(xdgCache, "burrow"),
		StateDir:   filepath.Join(xdgState, "burrow"),
		DataDir:    filepath.Join(xdgData, "burrow"),
		TrashDir:   filepath.Join(xdgData, "Trash"),
	}
}

// ExpandHome expands a leading "~" or "~/" using home.
func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

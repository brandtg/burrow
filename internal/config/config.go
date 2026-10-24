// Package config loads burrow's TOML configuration, applies the documented
// precedence (CLI flag > env > file > default), and resolves the effective
// paths.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the full config.toml schema (spec §13.2).
type Config struct {
	General   General   `toml:"general"`
	Clean     Clean     `toml:"clean"`
	Trash     Trash     `toml:"trash"`
	Purge     Purge     `toml:"purge"`
	Analyze   Analyze   `toml:"analyze"`
	Downloads Downloads `toml:"downloads"`
	Dev       Dev       `toml:"dev"`
	UserCache UserCache `toml:"user-cache"`
}

// General holds top-level UI settings.
type General struct {
	Color    string `toml:"color"`
	LogLevel string `toml:"log_level"`
}

// Clean holds cleaner defaults.
type Clean struct {
	DefaultCategories []string `toml:"default_categories"`
	JournaldMaxSize   string   `toml:"journald_max_size"`
	JournaldMaxAge    string   `toml:"journald_max_age"`
	MinSize           string   `toml:"min_size"`
	ProtectedPaths    []string `toml:"protected_paths"`
}

// Trash selects the trash backend.
type Trash struct {
	Backend         string `toml:"backend"`
	ExternalCommand string `toml:"external_command"`
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
		General: General{Color: "auto", LogLevel: "info"},
		Clean: Clean{
			DefaultCategories: []string{"projects", "dev", "user-cache", "desktop-pkgs", "downloads"},
			JournaldMaxSize:   "500M",
			JournaldMaxAge:    "",
			MinSize:           "0",
			ProtectedPaths:    []string{"/etc", "/usr", "/var/lib"},
		},
		Trash: Trash{Backend: "native", ExternalCommand: ""},
		Purge: Purge{
			Paths:        []string{"~/Projects", "~/src", "~/dev", "~/code", "~/work"},
			MaxAge:       "7d",
			ActiveWithin: "30d",
		},
		Analyze: Analyze{
			OneFileSystem: true,
			Ignore:        []string{"/proc", "/dev", "/sys"},
		},
		Downloads: Downloads{
			InstallerExtensions: []string{".deb", ".rpm", ".AppImage", ".iso", ".pkg.tar.zst", ".snap", ".flatpakref"},
			InstallerMinAge:     "30d",
			OldMinAge:           "90d",
			OldMinSize:          "100M",
		},
		Dev:       Dev{MaxAge: "30d", Preselect: false},
		UserCache: UserCache{SweepUnknown: false, MaxAge: "30d"},
	}
}

// Load reads path into a copy of the defaults. A missing file is not an error.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
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
	if v := getenv("BURROW_LOG_LEVEL"); v != "" {
		c.General.LogLevel = v
	}
	if v := getenv("BURROW_MIN_SIZE"); v != "" {
		c.Clean.MinSize = v
	}
	if v := getenv("BURROW_PROTECTED_PATHS"); v != "" {
		c.Clean.ProtectedPaths = splitList(v)
	}
	if v := getenv("BURROW_TRASH_BACKEND"); v != "" {
		c.Trash.Backend = v
	}
	if v := getenv("BURROW_PURGE_MAX_AGE"); v != "" {
		c.Purge.MaxAge = v
	}
	if v := getenv("BURROW_PURGE_PATHS"); v != "" {
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

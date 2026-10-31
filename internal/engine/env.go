package engine

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

// ResolveOptions lets callers inject the process environment for testing and
// override the config path.
type ResolveOptions struct {
	// Getenv defaults to os.Getenv.
	Getenv func(string) string
	// Geteuid defaults to os.Geteuid.
	Geteuid func() int
	// ConfigPath overrides the default config file location.
	ConfigPath string
	// DryRun is propagated to the owner runner.
	DryRun bool
	// Distro overrides /etc/os-release detection (tests, container matrix).
	Distro *distro.Info
	// Adapter overrides the adapter selected for the detected distro.
	Adapter distro.Adapter
}

func (o ResolveOptions) getenv() func(string) string {
	if o.Getenv != nil {
		return o.Getenv
	}
	return os.Getenv
}

func (o ResolveOptions) geteuid() func() int {
	if o.Geteuid != nil {
		return o.Geteuid
	}
	return os.Geteuid
}

// Resolve performs pipeline step 1: refuse root-without-sudo (I6), resolve the
// invoking user and XDG directories, load config and whitelist, and detect the
// distro. It is read-only.
func Resolve(opts ResolveOptions) (*Env, error) {
	getenv := opts.getenv()
	if err := owner.CheckRoot(opts.geteuid(), getenv); err != nil {
		return nil, err
	}
	u, err := owner.ResolveInvokingUser(getenv)
	if err != nil {
		return nil, err
	}
	elevated := owner.Elevated(getenv)
	dirs := owner.UserDirs(u, elevated, getenv)

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = filepath.Join(dirs.Config, "burrow", "config.toml")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	cfg.ApplyEnv(getenv)

	paths := config.ResolvePaths(dirs.Config, dirs.Data, dirs.State, dirs.Cache)
	wl, err := whitelist.Load(paths.Whitelist)
	if err != nil {
		return nil, err
	}

	var info distro.Info
	if opts.Distro != nil {
		info = *opts.Distro
	} else {
		detected, derr := distro.Detect()
		if derr != nil {
			detected = distro.Info{}
		}
		info = detected
	}
	adapter := opts.Adapter
	if adapter == nil {
		adapter = distro.For(info)
	}

	run := owner.New(opts.DryRun)
	run.UserEnv = owner.Environment(dirs)
	if elevated {
		// Per-user commands must run as the invoking user, never
		// with inherited root credentials.
		run.UserCredential = owner.Credential(u)
	}

	env := &Env{
		User:       u,
		Home:       dirs.Home,
		XDGCache:   dirs.Cache,
		XDGConfig:  dirs.Config,
		XDGData:    dirs.Data,
		XDGState:   dirs.State,
		XDGRuntime: dirs.Runtime,
		Run:        run,
		Whitelist:  wl,
		Cfg:        cfg,
		Distro:     info,
		Adapter:    adapter,
		Paths:      paths,
		DryRun:     opts.DryRun,
	}
	return env, nil
}

// UID returns the invoking user's numeric uid, or -1.
func (e *Env) UID() int {
	if e == nil || e.User == nil {
		return -1
	}
	n, err := strconv.Atoi(e.User.Uid)
	if err != nil {
		return -1
	}
	return n
}

// Trash returns the resolved home trash directory.
func (e *Env) Trash() string { return e.Paths.TrashDir }

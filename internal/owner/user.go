package owner

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/brandtg/burrow/internal/i18n"
)

// ResolveInvokingUser returns the user on whose behalf burrow is running. If
// elevated via sudo, SUDO_USER identifies the real user; otherwise the current
// uid is looked up. Root's own environment is never trusted for HOME/XDG.
func ResolveInvokingUser(getenv func(string) string) (*user.User, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if sudoUser := getenv("SUDO_USER"); sudoUser != "" {
		u, err := user.Lookup(sudoUser)
		if err != nil {
			return nil, fmt.Errorf(i18n.T(i18n.ErrUserResolve), err)
		}
		return u, nil
	}
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf(i18n.T(i18n.ErrUserResolve), err)
	}
	return u, nil
}

// Elevated reports whether SUDO_USER is set (i.e. we are running under sudo on
// behalf of another user).
func Elevated(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("SUDO_USER") != ""
}

// CheckRoot enforces invariant I6: never run the whole process as root. When
// euid is 0 and SUDO_USER is unset, the caller must refuse.
func CheckRoot(geteuid func() int, getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	if geteuid() == 0 && getenv("SUDO_USER") == "" {
		return fmt.Errorf("%s", i18n.T(i18n.ErrRootRefused))
	}
	return nil
}

// Dirs are the resolved per-user directories.
type Dirs struct {
	Home    string
	Cache   string
	Config  string
	Data    string
	State   string
	Runtime string
}

// Credential returns the process credential that runs a per-user command as u.
// It returns nil when u has no numeric uid/gid, so the caller fails closed
// rather than dropping to an unknown user.
func Credential(u *user.User) *syscall.Credential {
	if u == nil {
		return nil
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
}

// Environment returns the HOME and XDG variables for d, so a per-user command
// sees the same trees burrow resolved rather than root's environment.
func Environment(d Dirs) []string {
	return []string{
		"HOME=" + d.Home,
		"XDG_CACHE_HOME=" + d.Cache,
		"XDG_CONFIG_HOME=" + d.Config,
		"XDG_DATA_HOME=" + d.Data,
		"XDG_STATE_HOME=" + d.State,
		"XDG_RUNTIME_DIR=" + d.Runtime,
	}
}

// UserDirs computes the invoking user's HOME and XDG directories.
//
// When elevated, the XDG_* variables in the environment belong to root, so they
// are ignored and the invoking user's defaults are derived from their HOME.
//
// When not elevated, HOME is still taken from the passwd entry (u.HomeDir), not
// $HOME. This is deliberate: the passwd entry cannot be redirected by a spoofed
// environment. The trade-off is that a container or dev shell with a modified
// HOME resolves config/journal/trash under the passwd home rather than the
// shell's tree. Absolute XDG_* overrides are still honored.
func UserDirs(u *user.User, elevated bool, getenv func(string) string) Dirs {
	if getenv == nil {
		getenv = os.Getenv
	}
	home := u.HomeDir
	d := Dirs{
		Home:    home,
		Cache:   filepath.Join(home, ".cache"),
		Config:  filepath.Join(home, ".config"),
		Data:    filepath.Join(home, ".local", "share"),
		State:   filepath.Join(home, ".local", "state"),
		Runtime: filepath.Join("/run/user", u.Uid),
	}
	if elevated {
		return d
	}
	if v := getenv("XDG_CACHE_HOME"); filepath.IsAbs(v) {
		d.Cache = v
	}
	if v := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		d.Config = v
	}
	if v := getenv("XDG_DATA_HOME"); filepath.IsAbs(v) {
		d.Data = v
	}
	if v := getenv("XDG_STATE_HOME"); filepath.IsAbs(v) {
		d.State = v
	}
	if v := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(v) {
		d.Runtime = v
	}
	return d
}

// Package downloads implements the `downloads` cleanup category (spec §8.6):
// the installer and stale-content sweep. Installer files are matched by
// extension and age from the [downloads] config; the stale sweep lists large
// old entries for the user to pick.
package downloads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/catalog"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
)

// ID is the category identifier.
const ID = "downloads"

// Category enumerates download/installer targets.
type Category struct{}

// New returns the downloads category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "Downloads" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "Installers and stale downloads. Trashed, so recoverable; ages and extensions come from [downloads]."
}

// Discover enumerates installer files and stale Downloads entries.
func (*Category) Discover(_ context.Context, env *engine.Env) ([]engine.Target, error) {
	if env == nil || env.Home == "" {
		return nil, nil
	}
	exts, installerAge, oldAge, oldSize := downloadConfig(env)
	downloadsDir := filepath.Join(env.Home, "Downloads")

	var out []engine.Target
	seen := map[string]bool{}

	installer := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		out = append(out, installersFor(env, path)...)
	}

	for _, dir := range []string{env.Home, downloadsDir} {
		for _, path := range filesIn(dir) {
			if matchesInstaller(path, exts, installerAge) {
				installer(path)
			}
		}
	}
	for _, path := range filesIn(downloadsDir) {
		if seen[path] {
			continue
		}
		if fi, err := os.Stat(path); err == nil && fi.ModTime().Before(time.Now().Add(-oldAge)) && fi.Size() >= oldSize {
			seen[path] = true
			out = append(out, oldFor(env, path)...)
		}
	}
	return out, nil
}

func downloadConfig(env *engine.Env) (exts []string, installerAge, oldAge time.Duration, oldSize int64) {
	exts = []string{".deb", ".rpm", ".AppImage", ".iso", ".pkg.tar.zst", ".snap", ".flatpakref"}
	installerAge = 30 * 24 * time.Hour
	oldAge = 90 * 24 * time.Hour
	oldSize = 100 << 20
	if env.Cfg == nil {
		return
	}
	d := env.Cfg.Downloads
	if len(d.InstallerExtensions) > 0 {
		exts = d.InstallerExtensions
	}
	if v, err := config.ParseDuration(d.InstallerMinAge); err == nil && v > 0 {
		installerAge = v
	}
	if v, err := config.ParseDuration(d.OldMinAge); err == nil && v > 0 {
		oldAge = v
	}
	if v, err := config.ParseBytes(d.OldMinSize); err == nil && v > 0 {
		oldSize = v
	}
	return
}

func matchesInstaller(path string, exts []string, age time.Duration) bool {
	name := strings.ToLower(filepath.Base(path))
	ok := false
	for _, ext := range exts {
		if ext != "" && strings.HasSuffix(name, strings.ToLower(ext)) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.ModTime().Before(time.Now().Add(-age))
}

func installersFor(env *engine.Env, path string) []engine.Target {
	t, err := catalog.NewTrashTarget(env, catalog.TrashTargetOptions{
		ID: "downloads.installers." + filepath.Base(path), Category: ID,
		Display: "installer: " + filepath.Base(path), Path: path,
		Risk: engine.RiskLow, DefaultOn: true, Evidence: []string{"installer older than the configured age"},
	})
	if err != nil {
		return nil
	}
	return []engine.Target{t}
}

func oldFor(env *engine.Env, path string) []engine.Target {
	t, err := catalog.NewTrashTarget(env, catalog.TrashTargetOptions{
		ID: "downloads.old." + filepath.Base(path), Category: ID,
		Display: "stale download: " + filepath.Base(path), Path: path,
		Risk: engine.RiskMed, DefaultOn: false, Evidence: []string{"older and larger than the configured thresholds"},
	})
	if err != nil {
		return nil
	}
	return []engine.Target{t}
}

// filesIn returns the regular files directly inside dir.
func filesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, de.Name()))
	}
	return out
}

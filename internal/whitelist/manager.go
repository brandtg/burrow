package whitelist

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manager edits a newline-delimited whitelist file while preserving comments
// and blank lines. It is the backing store for `burrow clean --whitelist`.
type Manager struct {
	path   string
	lines  []string
	loaded bool
}

// NewManager returns a Manager for path.
func NewManager(path string) *Manager { return &Manager{path: path} }

// Path returns the backing file path.
func (m *Manager) Path() string { return m.path }

// Load reads the file. A missing file is treated as empty.
func (m *Manager) Load() error {
	m.lines = nil
	f, err := os.Open(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.loaded = true
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m.lines = append(m.lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return err
	}
	m.loaded = true
	return nil
}

// Entries returns the active entries, ignoring comments and blank lines.
func (m *Manager) Entries() []string {
	var out []string
	for _, line := range m.lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, filepath.Clean(line))
	}
	return out
}

// Add appends path if it is not already present. It reports whether the file
// changed.
func (m *Manager) Add(path string) (bool, error) {
	clean, err := validateEntry(path)
	if err != nil {
		return false, err
	}
	for _, e := range m.Entries() {
		if e == clean {
			return false, nil
		}
	}
	m.lines = append(m.lines, clean)
	return true, nil
}

// Remove deletes every active line equal to path. It reports whether the file
// changed. Comments are preserved.
func (m *Manager) Remove(path string) (bool, error) {
	clean, err := validateEntry(path)
	if err != nil {
		return false, err
	}
	var kept []string
	changed := false
	for _, line := range m.lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && filepath.Clean(trimmed) == clean {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if changed {
		m.lines = kept
	}
	return changed, nil
}

// Save writes the file atomically, preserving comments and blank lines. A
// partial write is never visible: the content is staged in a sibling temp file
// and renamed into place.
func (m *Manager) Save() error {
	if dir := filepath.Dir(m.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".whitelist-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	w := bufio.NewWriter(tmp)
	for _, line := range m.lines {
		if _, err := w.WriteString(line + "\n"); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, m.path)
}

func validateEntry(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("whitelist: empty path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("whitelist: entry is not absolute: %q", path)
	}
	return filepath.Clean(path), nil
}

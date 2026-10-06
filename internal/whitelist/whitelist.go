// Package whitelist loads and matches burrow's path whitelist.
//
// A whitelisted path protects itself and its parent from the generic user-cache
// sweep (spec §13.1). Matching is prefix-based on resolved physical paths.
//
// Provenance: independent reimplementation of Mole's whitelist semantics.
package whitelist

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Set is an immutable set of whitelisted absolute paths.
type Set struct {
	entries []string
}

// Load reads a newline-delimited whitelist file. Blank lines and lines starting
// with '#' are ignored. A missing file yields an empty set.
func Load(path string) (*Set, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Set{}, nil
		}
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads entries from r.
func Parse(r io.Reader) (*Set, error) {
	set := &Set{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			return nil, fmt.Errorf("whitelist: entry is not absolute: %q", line)
		}
		set.entries = append(set.entries, filepath.Clean(line))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

// Entries returns a copy of the whitelist entries.
func (s *Set) Entries() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.entries...)
}

// Empty reports whether the set has no entries.
func (s *Set) Empty() bool { return s == nil || len(s.entries) == 0 }

// Contains reports whether path is an entry or lies underneath one.
func (s *Set) Contains(path string) bool {
	if s == nil {
		return false
	}
	clean := filepath.Clean(path)
	for _, e := range s.entries {
		if clean == e || strings.HasPrefix(clean, e+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Protected reports whether path should be skipped by the generic sweep. It is
// protected if it is whitelisted or is the parent of a whitelisted entry.
func (s *Set) Protected(path string) bool {
	if s == nil {
		return false
	}
	if s.Contains(path) {
		return true
	}
	clean := filepath.Clean(path)
	for _, e := range s.entries {
		if filepath.Dir(e) == clean {
			return true
		}
	}
	return false
}

// Command safety-gate is the static "no direct destructive sink" gate (spec
// §14.5). It fails if os.Remove, os.RemoveAll, os.Truncate, or syscall.Unlink
// appear outside the allow-listed packages, mirroring Mole's
// audit_destructive_sinks.py.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// allowedPrefixes are the packages permitted to call destructive sinks.
var allowedPrefixes = []string{
	filepath.FromSlash("internal/safe"),
	filepath.FromSlash("internal/trash"),
	filepath.FromSlash("internal/owner"),
	filepath.FromSlash("scripts/safety-gate"),
}

// Violation is one disallowed sink.
type Violation struct {
	File   string
	Line   int
	Sink   string
	Reason string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", v.File, v.Line, v.Sink, v.Reason)
}

// sink describes a forbidden call.
type sink struct {
	pkg  string
	name string
}

var forbidden = []sink{
	{pkg: "os", name: "Remove"},
	{pkg: "os", name: "RemoveAll"},
	{pkg: "os", name: "Truncate"},
	{pkg: "syscall", name: "Unlink"},
}

// Check walks root and returns every disallowed sink call. Test files are
// skipped: the gate governs production mutation paths.
func Check(root string) ([]Violation, error) {
	var violations []Violation
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if isAllowed(rel) {
			return nil
		}
		fileViolations, err := checkFile(path, rel)
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
	return violations, nil
}

func isAllowed(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range allowedPrefixes {
		p = filepath.ToSlash(p)
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

func checkFile(path, rel string) ([]Violation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	imports := importNames(f)
	var out []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkgPath := imports[ident.Name]
		for _, s := range forbidden {
			if pkgPath == s.pkg && sel.Sel.Name == s.name {
				out = append(out, Violation{
					File:   rel,
					Line:   fset.Position(call.Pos()).Line,
					Sink:   s.pkg + "." + s.name,
					Reason: "destructive sink outside safe/trash/owner",
				})
			}
		}
		return true
	})
	return out, nil
}

// importNames maps the local package identifier to its import path.
func importNames(f *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := filepath.Base(path)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		names[local] = path
	}
	return names
}

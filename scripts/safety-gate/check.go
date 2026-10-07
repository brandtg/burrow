// Command safety-gate is the static "no direct destructive sink" gate (spec
// §14.5). It fails if a destructive sink (os.Remove/RemoveAll/Truncate,
// syscall.Unlink/Unlinkat/Rmdir/Ftruncate/Truncate, or the x/sys/unix
// equivalents) appears outside the allow-listed packages, mirroring Mole's
// audit_destructive_sinks.py.
//
// It additionally enforces a narrower rule for safe.RemoveTree, the permanent
// delete primitive: it may be called only from internal/purge (and is defined
// in internal/safe). This keeps a trash-contract caller from silently
// downgrading a recoverable delete to a permanent one (invariant I5). The rule
// is checked even inside the packages that are allowed general destructive
// sinks.
//
// Limitations: the gate is purely syntactic. It also flags method calls named
// Truncate (e.g. (*os.File).Truncate) regardless of receiver, which can
// false-positive on bytes.Buffer.Truncate. It handles package aliases and
// dot-imports, but a sink assigned to a variable first (remove := os.Remove)
// is not detected; keep sinks behind internal/safe, internal/trash, and
// internal/owner.
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

// safePkgPath is the import path of the safety core. safe.RemoveTree is the
// single permanent-delete primitive, so calls to it are governed by a narrower
// allow-list than the general destructive sinks.
const safePkgPath = "github.com/brandtg/burrow/internal/safe"

// removeTreeAllowedPrefixes are the packages permitted to call
// safe.RemoveTree. Only the purge path may delete permanently; confining it here
// means a later trash-contract caller cannot silently downgrade a recoverable
// delete to a permanent one (invariant I5). internal/safe defines the primitive.
var removeTreeAllowedPrefixes = []string{
	filepath.FromSlash("internal/purge"),
	filepath.FromSlash("internal/safe"),
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
	{pkg: "syscall", name: "Unlinkat"},
	{pkg: "syscall", name: "Rmdir"},
	{pkg: "syscall", name: "Ftruncate"},
	{pkg: "syscall", name: "Truncate"},
	{pkg: "golang.org/x/sys/unix", name: "Unlink"},
	{pkg: "golang.org/x/sys/unix", name: "Unlinkat"},
	{pkg: "golang.org/x/sys/unix", name: "Rmdir"},
	{pkg: "golang.org/x/sys/unix", name: "Ftruncate"},
	{pkg: "golang.org/x/sys/unix", name: "Truncate"},
}

// forbiddenMethods are destructive methods whose receiver type is not
// statically known (e.g. (*os.File).Truncate). They are flagged regardless of
// receiver; a false positive on bytes.Buffer.Truncate is preferable to a missed
// file truncation.
var forbiddenMethods = []string{"Truncate"}

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
		// The RemoveTree confinement rule applies even inside packages that
		// are allowed general destructive sinks (trash/owner), because those
		// packages must never reach for the permanent-delete primitive.
		if !removeTreeAllowed(rel) {
			rtViolations, err := checkRemoveTree(path, rel)
			if err != nil {
				return err
			}
			violations = append(violations, rtViolations...)
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

func removeTreeAllowed(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range removeTreeAllowedPrefixes {
		p = filepath.ToSlash(p)
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// checkRemoveTree flags calls to safe.RemoveTree outside internal/purge (and the
// package that defines it). It understands import aliases and dot-imports.
func checkRemoveTree(path, rel string) ([]Violation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	imports := importNames(f)
	dotSafe := false
	for _, imp := range f.Imports {
		if imp.Name != nil && imp.Name.Name == "." && strings.Trim(imp.Path.Value, `"`) == safePkgPath {
			dotSafe = true
		}
	}
	var out []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if ident, ok := fun.X.(*ast.Ident); ok && fun.Sel.Name == "RemoveTree" && imports[ident.Name] == safePkgPath {
				out = append(out, removeTreeViolation(rel, fset, call))
			}
		case *ast.Ident:
			if dotSafe && fun.Name == "RemoveTree" {
				out = append(out, removeTreeViolation(rel, fset, call))
			}
		}
		return true
	})
	return out, nil
}

func removeTreeViolation(rel string, fset *token.FileSet, call *ast.CallExpr) Violation {
	return Violation{
		File:   rel,
		Line:   fset.Position(call.Pos()).Line,
		Sink:   "safe.RemoveTree",
		Reason: "permanent delete outside internal/purge",
	}
}

func checkFile(path, rel string) ([]Violation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	imports := importNames(f)
	var dotPkgs []string
	for _, imp := range f.Imports {
		if imp.Name != nil && imp.Name.Name == "." {
			dotPkgs = append(dotPkgs, strings.Trim(imp.Path.Value, `"`))
		}
	}
	var out []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if ident, ok := fun.X.(*ast.Ident); ok {
				if s, ok := matchSink(imports[ident.Name], fun.Sel.Name); ok {
					out = append(out, violationAt(rel, fset, call, s.pkg+"."+s.name))
					return true
				}
			}
			for _, m := range forbiddenMethods {
				if fun.Sel.Name == m {
					out = append(out, violationAt(rel, fset, call, "(method)."+m))
					return true
				}
			}
		case *ast.Ident:
			for _, s := range forbidden {
				if fun.Name == s.name && containsString(dotPkgs, s.pkg) {
					out = append(out, violationAt(rel, fset, call, s.pkg+"."+s.name))
				}
			}
		}
		return true
	})
	return out, nil
}

// matchSink reports whether pkgPath.name is a forbidden package-level sink.
func matchSink(pkgPath, name string) (sink, bool) {
	for _, s := range forbidden {
		if s.pkg == pkgPath && s.name == name {
			return s, true
		}
	}
	return sink{}, false
}

func violationAt(rel string, fset *token.FileSet, call *ast.CallExpr, name string) Violation {
	return Violation{
		File:   rel,
		Line:   fset.Position(call.Pos()).Line,
		Sink:   name,
		Reason: "destructive sink outside safe/trash/owner",
	}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
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

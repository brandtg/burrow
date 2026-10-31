// Command safety-gate is the static "no direct destructive sink" gate. It
// fails if a destructive sink (os.Remove/RemoveAll/Truncate,
// syscall.Unlink/Unlinkat/Rmdir/Ftruncate/Truncate, or the x/sys/unix
// equivalents) appears outside the allow-listed packages. The concept mirrors
// Mole's audit_destructive_sinks.py; this is an independent implementation and
// no Mole source is copied.
//
// It additionally flags delete-capable external commands in owner argv tables
// (rm, find -delete/-exec, truncate, shred, dd, xargs rm, shell -c). The AST
// gate cannot see inside an external command, so without this rule a catalog
// could shell out a permanent delete and bypass internal/safe's validation and
// identity checks entirely. This rule applies in every package, including
// internal/owner.
//
// Limitations: the gate is purely syntactic. It also flags method calls named
// Truncate (e.g. (*os.File).Truncate) regardless of receiver, which can
// false-positive on bytes.Buffer.Truncate. It handles package aliases and
// dot-imports, but a sink assigned to a variable first (remove := os.Remove)
// is not detected; keep sinks behind internal/safe, internal/trash, and
// internal/owner. Owner argv tables are only inspected when the Argv field is
// a fully literal []string; an argv assembled from a variable is not seen.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
		// Owner argv tables are checked in every package, including the
		// allowed sink packages: a shelled delete bypasses internal/safe even
		// when the calling package is permitted raw os sinks.
		argvViolations, err := checkArgvTables(path, rel)
		if err != nil {
			return err
		}
		violations = append(violations, argvViolations...)
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

// forbiddenArgvCommands are commands that can delete or truncate files. They
// must never be the command in an owner argv table: the AST sink gate cannot
// see inside an external command, so a shelled delete would bypass internal/
// safe's path validation and identity checks entirely.
var forbiddenArgvCommands = map[string]bool{
	"rm": true, "shred": true, "dd": true, "truncate": true,
	"unlink": true, "wipefs": true, "mkfs": true, "srm": true,
}

// shellCommands are interpreters; `sh -c ...` in an owner argv defeats the
// exact-argv discipline (invariant I4) and can hide any command.
var shellCommands = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true}

// dangerousFindFlags turn a read-only find into a delete.
var dangerousFindFlags = map[string]bool{"-delete": true, "-exec": true, "-execdir": true}

// checkArgvTables flags delete-capable external commands in owner argv tables.
// An argv is a composite-literal field named Argv whose value is a fully
// literal []string. Only literal elements are inspected; an argv assembled from
// a variable is not seen (the same limitation as the rest of the gate).
func checkArgvTables(path, rel string) ([]Violation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Argv" {
				continue
			}
			slice, ok := kv.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			argv, complete := literalStrings(slice)
			if !complete {
				continue
			}
			if reason := argvDeleteReason(argv); reason != "" {
				out = append(out, Violation{
					File:   rel,
					Line:   fset.Position(kv.Pos()).Line,
					Sink:   "owner argv",
					Reason: reason,
				})
			}
		}
		return true
	})
	return out, nil
}

// literalStrings returns the string elements of a composite literal, or
// complete=false if any element is not a plain string literal.
func literalStrings(lit *ast.CompositeLit) ([]string, bool) {
	out := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		bl, ok := elt.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return nil, false
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// argvDeleteReason reports why an owner argv is delete-capable, or "".
func argvDeleteReason(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	cmd := filepath.Base(argv[0])
	if forbiddenArgvCommands[cmd] {
		return "delete-capable command in owner argv: " + cmd
	}
	if shellCommands[cmd] {
		for _, a := range argv[1:] {
			if a == "-c" {
				return "shell invocation in owner argv: " + cmd + " -c"
			}
		}
	}
	for _, a := range argv[1:] {
		switch {
		case cmd == "find" && dangerousFindFlags[a]:
			return "delete-capable find in owner argv: " + a
		case cmd == "xargs" && forbiddenArgvCommands[filepath.Base(a)]:
			return "delete-capable xargs in owner argv: " + a
		}
	}
	return ""
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

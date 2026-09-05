// Package singlecaller reports short, unexported functions and methods that
// are directly called from exactly one call site within their own package —
// usually a sign the indirection isn't earning its keep and should just be
// inlined at that call site. A common way this shows up: a row-to-model
// builder copied from a convention that only pays for itself when a code
// generator produces several distinct row types for one entity — legitimate
// when there really are several shapes to reconcile, pointless when there's
// only ever one row type and therefore one caller. See testdata/demo and
// BLOG.html for a worked example.
//
// Three things keep this from just flagging every single-use helper, which
// would otherwise drown in false positives from HTTP handlers, goroutine
// entry points, and other legitimate one-call extractions:
//
//  1. Only direct calls (f(...), x.f(...)) count as a use. A function
//     passed by value — router.Get("/x", handleX), go worker() — is a
//     required extraction, not an inlinable one, so those don't count.
//  2. Only short functions (see maxInlinableLines) are flagged. A one-call
//     50-line function is very likely a deliberate readability split, not
//     the "just return a struct literal" pattern this is aimed at.
//  3. Only call sites that aren't already complex (see maxCallerComplexity)
//     are flagged. A trivial helper is only worth inlining if the result is
//     still readable — dropping it into a call site that's already got a
//     dozen branches just relocates the complexity instead of removing it.
//
// It only checks unexported symbols. An exported func/method might have
// callers in other packages that a single-package pass can't see, so "one
// use" isn't a safe signal there — this only reasons about names no other
// package could possibly reference.
//
// See the repo root for how to run this — directly, as a go vet tool, or
// bundled with every other slopless-go check.
package singlecaller

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// maxInlinableLines is the line-count ceiling for flagging a single-use
// function. Above this, a one-call extraction is more likely a deliberate
// readability split (e.g. a sizeable HTTP handler or setup routine) than the
// "trivial builder with no other caller" pattern this tool targets.
const maxInlinableLines = 15

// maxCallerComplexity is the cyclomatic-complexity ceiling for the function
// a candidate is called from. Above this, the call site already has enough
// branching that adding a few more inlined lines makes it harder to read,
// not easier — the inlining would just move the complexity, not remove it.
const maxCallerComplexity = 15

var Analyzer = &analysis.Analyzer{
	Name: "singlecaller",
	Doc:  "reports short, unexported funcs/methods called from exactly one site in their package",
	Run:  run,
}

type declaration struct {
	obj  *types.Func
	node *ast.FuncDecl
	name string
}

func run(pass *analysis.Pass) (any, error) {
	// Declarations are collected only from hand-written files — flagging a
	// generated file would just be noise, since it's never the one you'd
	// edit to inline anything.
	declared := map[*types.Func]*declaration{}
	for _, f := range pass.Files {
		if isGenerated(f) {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name == "_" || ast.IsExported(fd.Name.Name) {
				continue
			}
			if fd.Recv == nil && (fd.Name.Name == "init" || fd.Name.Name == "main") {
				continue
			}
			if lineCount(pass, fd) > maxInlinableLines {
				continue
			}
			obj, _ := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if obj == nil {
				continue
			}
			declared[obj] = &declaration{obj: obj, node: fd, name: fd.Name.Name}
		}
	}
	if len(declared) == 0 {
		return nil, nil
	}

	// Count direct calls across every file in the package, generated ones
	// included — a generated file calling a hand-written helper still
	// counts as a real call site. A bare reference to the function (passed
	// as a value, not invoked) does not count — see the package doc.
	//
	// Walked per enclosing FuncDecl (rather than one flat pass per file) so
	// each call site's complexity can be attributed to whichever candidate
	// it calls. A candidate's complexity is only meaningful once — it's
	// only read back when counts[obj] ends up exactly 1.
	counts := map[*types.Func]int{}
	callerComplexity := map[*types.Func]int{}
	for _, f := range pass.Files {
		for _, d := range f.Decls {
			caller, ok := d.(*ast.FuncDecl)
			if !ok || caller.Body == nil {
				continue
			}
			complexity := -1 // computed lazily, only if this caller actually calls a candidate
			ast.Inspect(caller.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident := calleeIdent(call.Fun)
				if ident == nil {
					return true
				}
				obj, ok := pass.TypesInfo.Uses[ident].(*types.Func)
				if !ok {
					return true
				}
				if _, isDeclared := declared[obj]; !isDeclared {
					return true
				}
				counts[obj]++
				if complexity < 0 {
					complexity = cyclomaticComplexity(caller)
				}
				callerComplexity[obj] = complexity
				return true
			})
		}
	}

	for obj, d := range declared {
		if counts[obj] != 1 {
			continue
		}
		if cc := callerComplexity[obj]; cc > maxCallerComplexity {
			continue
		}
		pass.Reportf(d.node.Pos(), "%s is called from a single site (call site complexity %d) — consider inlining it there instead",
			d.name, callerComplexity[obj])
	}

	return nil, nil
}

// cyclomaticComplexity is a standard McCabe-style count: one branch point
// (if, for, range, a non-default case, &&, ||) adds one, starting from a
// base of one path through the function.
func cyclomaticComplexity(fd *ast.FuncDecl) int {
	complexity := 1
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt:
			complexity++
		case *ast.ForStmt:
			complexity++
		case *ast.RangeStmt:
			complexity++
		case *ast.CaseClause:
			if x.List != nil { // nil List is the default case — no branch added
				complexity++
			}
		case *ast.CommClause:
			if x.Comm != nil { // nil Comm is the default case in a select
				complexity++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				complexity++
			}
		}
		return true
	})
	return complexity
}

// calleeIdent extracts the identifier actually being invoked from a call
// expression's Fun — the bare name for a free function (f(...)) or the
// selector's Sel for a method (x.f(...)).
func calleeIdent(fun ast.Expr) *ast.Ident {
	switch e := fun.(type) {
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return e.Sel
	default:
		return nil
	}
}

// lineCount returns a FuncDecl's source line span, body included.
func lineCount(pass *analysis.Pass, fd *ast.FuncDecl) int {
	start := pass.Fset.Position(fd.Pos()).Line
	end := pass.Fset.Position(fd.End()).Line
	return end - start + 1
}

// isGenerated matches the "// Code generated ... DO NOT EDIT." marker
// convention most Go code generators use (the one gofmt itself recognizes).
func isGenerated(f *ast.File) bool {
	for _, c := range f.Comments {
		for _, l := range c.List {
			if strings.Contains(l.Text, "Code generated") && strings.Contains(l.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

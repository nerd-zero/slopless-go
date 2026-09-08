// Package duphelper reports top-level functions that are structurally
// identical to another function already declared in the same package —
// usually the result of hitting a "<name> redeclared in this block" compile
// error (or just not noticing an existing helper) and writing a second copy
// under a new name, rather than reusing the first. A real example that
// motivated this tool: a test file needed a `func(float64) *float64`
// pointer-of helper, one already existed in a sibling file in the same
// package, and — likely to dodge a redeclaration error, or simply because it
// wasn't found — a second, identically-bodied one got added under a
// different name. See testdata/demo for a worked (fictional) example.
//
// Two functions are considered duplicates when:
//
//  1. Both are free functions (no receiver — see Known limitations).
//  2. Their signatures match exactly by *type*, parameter and result names
//     ignored: `func(v float64) *float64` and `func(x float64) *float64`
//     match; `func(v float64) *float64` and `func(v float32) *float64` do
//     not, and neither does a different result count or a differing
//     variadic-ness.
//  3. Their bodies are alpha-equivalent: statement-for-statement and
//     expression-for-expression identical once every local — parameters,
//     named results, and anything declared inside the body (:=, var, a
//     range/type-switch variable, a nested closure's own parameters) — is
//     replaced by a placeholder keyed to its *binding position* (parameters
//     and named results first, left to right, exactly as the signature
//     check already lines them up; then body-declared locals in the order
//     they're first bound) rather than its literal name. Binding position
//     — not order of first use in the body — is what parameters are keyed
//     on, because a function can read its parameters in any order: without
//     this, `func(a, b int) int { return a - b }` and
//     `func(a, b int) int { return b - a }` would wrongly canonicalize the
//     same way (both would bind whichever operand appears textually first
//     to the first placeholder), silently flattening a real behavioral
//     difference into a false "duplicate."
//     Anything that is NOT local — a package-level name, an imported
//     identifier, a struct field or method selector, a literal's exact
//     value — must still match exactly. Two functions with the same shape
//     that read different globals, call different things, or return
//     different constants are not duplicates.
//
// Comments never affect the comparison.
//
// # Known limitations
//
//   - Methods (functions with a receiver) aren't compared. Two methods can
//     have identical bodies for a good reason (a matched pair implementing
//     two different interfaces), and comparing receiver types correctly
//     alongside everything else adds real complexity — not attempted yet.
//   - Generic functions (with type parameters) are never compared; two
//     instantiations of a shape that's already generic aren't the problem
//     this tool is for.
//   - This is a single-package pass. A local reimplementation of something
//     that already exists as an *exported* helper in an imported package
//     (the "we already have a helpers package for this" case) isn't caught
//     — only two declarations sitting in the same package are compared.
//   - Labels (goto targets) are compared by their literal text, not
//     alpha-renamed. Two functions that are otherwise identical but use
//     differently-named labels for the same control flow won't match — a
//     missed finding, never a false one.
//   - A function with an empty body (no statements) is never a candidate —
//     matching stubs aren't an interesting finding.
//
// See the repo root for how to run this — directly, as a go vet tool, or
// bundled with every other slopless-go check.
package duphelper

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "duphelper",
	Doc:  "reports top-level functions structurally identical to another function already declared in the same package",
	Run:  run,
}

type candidate struct {
	fd   *ast.FuncDecl
	name string
}

func run(pass *analysis.Pass) (any, error) {
	// Only hand-written files contribute candidates — a generated file is
	// never the one you'd edit to remove a duplicate from, so flagging one
	// (or comparing two generated duplicates against each other) is just
	// noise. Mirrors singlecaller's own reasoning.
	groups := map[string][]candidate{}
	for _, f := range pass.Files {
		if isGenerated(f) {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || len(fd.Body.List) == 0 {
				continue
			}
			obj, _ := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if obj == nil {
				continue
			}
			sig, ok := obj.Type().(*types.Signature)
			if !ok || (sig.TypeParams() != nil && sig.TypeParams().Len() > 0) {
				continue
			}
			key := signatureKey(pass, sig) + "\x00" + canonicalBody(pass, fd)
			groups[key] = append(groups[key], candidate{fd: fd, name: fd.Name.Name})
		}
	}

	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		// Sorted by (filename, line, col) rather than raw token.Pos: Pos is a
		// FileSet-wide offset ordered by the order files were parsed, which
		// has no relation to filename or line number a reader would expect
		// "declared first" to mean — sorting on that would make which
		// declaration gets called "the original" look arbitrary.
		sort.Slice(group, func(i, j int) bool {
			pi, pj := pass.Fset.Position(group[i].fd.Pos()), pass.Fset.Position(group[j].fd.Pos())
			if pi.Filename != pj.Filename {
				return pi.Filename < pj.Filename
			}
			return pi.Line < pj.Line
		})
		original := group[0]
		for _, dup := range group[1:] {
			pass.Reportf(dup.fd.Pos(),
				"%s is structurally identical to %s (declared at %s) — reuse it instead of redeclaring",
				dup.name, original.name, pass.Fset.Position(original.fd.Pos()))
		}
	}

	return nil, nil
}

// signatureKey canonicalizes a signature by type only: parameter and result
// *names* never affect it (that's the whole point — ptrFloat64(v float64)
// and ptrFloat64Cofid(v float64) must key the same), but every type,
// variadic-ness, and result count must match exactly.
func signatureKey(pass *analysis.Pass, sig *types.Signature) string {
	qualifier := types.RelativeTo(pass.Pkg)
	tuple := func(t *types.Tuple) string {
		parts := make([]string, t.Len())
		for i := range parts {
			parts[i] = types.TypeString(t.At(i).Type(), qualifier)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprintf("params:%s|variadic:%v|results:%s", tuple(sig.Params()), sig.Variadic(), tuple(sig.Results()))
}

// canonicalizer renders one function body to an alpha-equivalence-normalized
// string: every local identifier prints as a positional placeholder, every
// non-local identifier prints as its literal name, and every other node
// prints by structurally walking its fields via reflection — so no AST node
// kind needs its own hand-written case here.
type canonicalizer struct {
	pass   *analysis.Pass
	fd     *ast.FuncDecl
	locals map[types.Object]string
}

func canonicalBody(pass *analysis.Pass, fd *ast.FuncDecl) string {
	c := &canonicalizer{pass: pass, fd: fd, locals: map[types.Object]string{}}
	// Parameters and named results are bound to placeholders by their
	// declared position BEFORE the body is walked — see the package doc for
	// why this must happen first rather than in order of first use.
	c.bindFields(fd.Type.Params)
	c.bindFields(fd.Type.Results)
	return c.render(fd.Body)
}

func (c *canonicalizer) bindFields(fl *ast.FieldList) {
	if fl == nil {
		return
	}
	for _, field := range fl.List {
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			if obj := c.pass.TypesInfo.ObjectOf(name); obj != nil {
				c.localToken(obj)
			}
		}
	}
}

func (c *canonicalizer) localToken(obj types.Object) string {
	if tok, ok := c.locals[obj]; ok {
		return tok
	}
	tok := fmt.Sprintf("L%d", len(c.locals))
	c.locals[obj] = tok
	return tok
}

// isLocalTo reports whether obj was declared somewhere inside fd's own
// source span — true for parameters, named results, and anything bound in
// the body (:=, var, range/type-switch variables, a nested closure's own
// parameters). Everything else — package-level names, imports, builtins —
// has a position outside that span (or none at all) and is left as-is.
func isLocalTo(fd *ast.FuncDecl, obj types.Object) bool {
	if obj == nil {
		return false
	}
	pos := obj.Pos()
	return pos.IsValid() && fd.Pos() <= pos && pos < fd.End()
}

var (
	posType   = reflect.TypeOf(token.NoPos)
	tokenType = reflect.TypeOf(token.ILLEGAL)
)

func (c *canonicalizer) render(n ast.Node) string {
	if n == nil {
		return "nil"
	}
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return "nil"
	}

	switch x := n.(type) {
	case *ast.Ident:
		return c.renderIdent(x)
	case *ast.BasicLit:
		return "Lit(" + x.Kind.String() + ":" + x.Value + ")"
	case *ast.CommentGroup, *ast.Comment:
		return "" // comments never affect structural equality
	}

	rv := reflect.ValueOf(n)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	t := rv.Type()
	var b strings.Builder
	b.WriteString(t.Name())
	b.WriteByte('(')
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		if ft.PkgPath != "" || ft.Type == posType {
			continue // unexported resolver-internal field, or a source position — neither affects equality
		}
		b.WriteString(c.renderValue(rv.Field(i)))
		b.WriteByte(';')
	}
	b.WriteByte(')')
	return b.String()
}

func (c *canonicalizer) renderIdent(id *ast.Ident) string {
	if id.Name == "_" {
		return "_"
	}
	if obj := c.pass.TypesInfo.ObjectOf(id); isLocalTo(c.fd, obj) {
		return c.localToken(obj)
	}
	return "id:" + id.Name
}

func (c *canonicalizer) renderValue(fv reflect.Value) string {
	switch fv.Kind() {
	case reflect.Ptr:
		if fv.IsNil() {
			return "nil"
		}
		if n, ok := fv.Interface().(ast.Node); ok {
			return c.render(n)
		}
		return "nil"
	case reflect.Interface:
		if fv.IsNil() {
			return "nil"
		}
		if n, ok := fv.Interface().(ast.Node); ok {
			return c.render(n)
		}
		return fmt.Sprintf("%v", fv.Interface())
	case reflect.Slice:
		parts := make([]string, fv.Len())
		for i := range parts {
			parts[i] = c.renderValue(fv.Index(i))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case reflect.Struct:
		if fv.CanAddr() {
			if n, ok := fv.Addr().Interface().(ast.Node); ok {
				return c.render(n)
			}
		}
		return fmt.Sprintf("%v", fv.Interface())
	case reflect.Bool:
		return fmt.Sprintf("%v", fv.Bool())
	case reflect.String:
		return fv.String()
	default:
		if fv.Type() == tokenType {
			return token.Token(fv.Int()).String()
		}
		if fv.CanInt() {
			return fmt.Sprintf("%d", fv.Int())
		}
		return fmt.Sprintf("%v", fv.Interface())
	}
}

// isGenerated matches the "// Code generated ... DO NOT EDIT." marker
// convention most Go code generators use (the one gofmt itself recognizes).
func isGenerated(f *ast.File) bool {
	for _, cg := range f.Comments {
		for _, l := range cg.List {
			if strings.Contains(l.Text, "Code generated") && strings.Contains(l.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

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
//  3. Their bodies produce the same control flow graph, once each is
//     compiled to SSA (static single assignment) form — see below.
//
// # Why SSA instead of syntax
//
// An earlier version of this tool compared the *syntax tree* of each
// function body, alpha-renaming locals to placeholders. That catches a
// renamed-copy-paste, but nothing else: a `for` loop and an equivalent
// `for range` loop are different syntax trees, so two functions that do
// the same work via different Go control-flow constructs looked unrelated
// to it, even though they compile down to the same machine behavior.
//
// This version instead builds each candidate function's SSA form — via
// golang.org/x/tools/go/ssa, the same intermediate representation
// go/analysis-based tools like staticcheck build on — and compares the
// resulting control flow graphs. SSA construction strips away syntax
// sugar: a counting `for` loop and a `for range` loop over the same
// collection, doing the same work per element, generally lower to the
// same sequence of blocks and instructions, so this version can catch
// them as duplicates where the syntax-tree version could not. It also
// normalizes constants: `0xA` and `10` are different literals in source
// but the same SSA constant, so functions differing only in how a literal
// was spelled now match too.
//
// The comparison walks each function's basic blocks and instructions in a
// deterministic order — a depth-first traversal from the entry block,
// following each block's successors in the order go/ssa recorded them
// (which is semantically meaningful: for an `If`, the first successor is
// always the true branch) — and renders that walk to a string. Every
// locally-defined value (a parameter, a free variable of a closure, or the
// result of an instruction) prints as a placeholder keyed to the position
// it's first bound in that traversal, exactly as the old syntax-based
// version keyed locals to their binding position rather than their name.
// Anything that isn't local — a call to a package-level function, a
// reference to a global, a field index, an operator, a type — must still
// match exactly.
//
// Two functions match when their rendered strings are identical. This is
// a *canonical-form* comparison, not general graph isomorphism: general
// graph isomorphism is expensive to compute and, for arbitrary graphs, not
// known to be solvable in polynomial time. Comparing canonical forms
// avoids that cost — it's a linear walk of each function — at the cost of
// occasionally missing a match that a full isomorphism search would find
// (see Known limitations). It never produces a false match: the
// traversal order is derived from real control-flow semantics (which
// branch is "true", which block is the loop header), not an arbitrary
// tie-break, so two functions that render identically really do run the
// same operations in the same order along every path.
//
// Comments never affect the comparison (they were never part of the AST
// comparison either, and SSA doesn't retain them at all).
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
//   - The traversal order is fixed by each block's successor order, which
//     is meaningful (true-branch-first for an `If`) but not symmetric: two
//     functions that are otherwise identical but swap which branch does
//     which — `if a { x() } else { y() }` versus the same code with the
//     condition inverted and the branches swapped — render differently and
//     won't match, even though they behave identically. A missed finding,
//     never a false one.
//   - A function with an empty body (no statements) is never a candidate —
//     matching stubs aren't an interesting finding.
//
// See the repo root for how to run this — directly, as a go vet tool, or
// bundled with every other slopless-go check.
package duphelper

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

var Analyzer = &analysis.Analyzer{
	Name:     "duphelper",
	Doc:      "reports top-level functions structurally identical to another function already declared in the same package",
	Requires: []*analysis.Analyzer{buildssa.Analyzer},
	Run:      run,
}

type candidate struct {
	fd   *ast.FuncDecl
	name string
}

func run(pass *analysis.Pass) (any, error) {
	ssaInput := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)

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
			fn := ssaInput.Pkg.Prog.FuncValue(obj)
			if fn == nil || len(fn.Blocks) == 0 {
				continue // no SSA body to compare (shouldn't happen for a non-empty, non-generic free function)
			}
			key := signatureKey(pass, sig) + "\x00" + canonicalSSA(pass, fn)
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

// ssaCanon renders one function's SSA control flow graph to a canonical
// string: every locally-defined value (parameter, free variable, or
// instruction result) prints as a placeholder keyed to the position it's
// first bound in a deterministic traversal, and every basic block prints
// as a placeholder keyed to the order that same traversal first reaches
// it — so two functions built from different source but the same
// underlying operations render identically, and go/ssa's own arbitrary
// register numbers and block indices (which are not guaranteed stable or
// meaningful across two independently-built functions) never leak into
// the comparison.
//
// One ssaCanon is used for an entire top-level candidate, including any
// closures nested inside it (a MakeClosure's function operand is rendered
// by recursing into renderFunc), so token numbering runs continuously
// across the whole tree — that's fine, since the same traversal produces
// the same numbering for either side of a comparison.
type ssaCanon struct {
	pass    *analysis.Pass
	qual    types.Qualifier
	values  map[ssa.Value]string
	nvalues int
	blocks  map[*ssa.BasicBlock]int
	nblocks int
}

func canonicalSSA(pass *analysis.Pass, fn *ssa.Function) string {
	c := &ssaCanon{
		pass:   pass,
		qual:   types.RelativeTo(pass.Pkg),
		values: map[ssa.Value]string{},
		blocks: map[*ssa.BasicBlock]int{},
	}
	return c.renderFunc(fn)
}

func (c *ssaCanon) bindValue(v ssa.Value) string {
	if tok, ok := c.values[v]; ok {
		return tok
	}
	tok := fmt.Sprintf("V%d", c.nvalues)
	c.nvalues++
	c.values[v] = tok
	return tok
}

func (c *ssaCanon) blockID(b *ssa.BasicBlock) int {
	if id, ok := c.blocks[b]; ok {
		return id
	}
	id := c.nblocks
	c.nblocks++
	c.blocks[b] = id
	return id
}

// blockOrder returns fn's reachable blocks in the deterministic order the
// comparison uses: a depth-first walk from the entry block following each
// block's successors in go/ssa's own recorded order, plus fn.Recover (if
// present) as a second root — Recover is reached only via an implicit
// panic-recovery edge, not through any block's ordinary Succs, so it needs
// its own walk to be included at all. Assigns each block's canonical ID
// as it's first visited.
func (c *ssaCanon) blockOrder(fn *ssa.Function) []*ssa.BasicBlock {
	var order []*ssa.BasicBlock
	seen := map[*ssa.BasicBlock]bool{}
	var visit func(b *ssa.BasicBlock)
	visit = func(b *ssa.BasicBlock) {
		if b == nil || seen[b] {
			return
		}
		seen[b] = true
		c.blockID(b)
		order = append(order, b)
		for _, s := range b.Succs {
			visit(s)
		}
	}
	if len(fn.Blocks) > 0 {
		visit(fn.Blocks[0])
	}
	visit(fn.Recover)
	return order
}

// renderFunc renders fn's parameters, free variables (non-empty only for a
// nested closure), and control flow graph. See the ssaCanon doc comment
// for the overall approach.
func (c *ssaCanon) renderFunc(fn *ssa.Function) string {
	// Parameters and free variables are bound to placeholders by their
	// declared position BEFORE the body is walked, exactly as the old
	// AST-based version bound parameters first — a function can read them
	// in any order, so they must be keyed by position, not by first use.
	for _, p := range fn.Params {
		c.bindValue(p)
	}
	for _, fv := range fn.FreeVars {
		c.bindValue(fv)
	}

	order := c.blockOrder(fn)

	// Every instruction-defined value is bound to a placeholder in
	// canonical block/instruction order BEFORE anything is rendered: a Phi
	// at a loop header can reference a value produced in the loop body, a
	// block this traversal doesn't reach until later.
	for _, b := range order {
		for _, instr := range b.Instrs {
			if _, ok := instr.(*ssa.DebugRef); ok {
				continue // pure debug metadata; never affects equality, and not even emitted without GlobalDebug
			}
			if v, ok := instr.(ssa.Value); ok {
				c.bindValue(v)
			}
		}
	}

	var out strings.Builder
	for _, b := range order {
		fmt.Fprintf(&out, "B%d(", c.blockID(b))
		for _, instr := range b.Instrs {
			if _, ok := instr.(*ssa.DebugRef); ok {
				continue
			}
			out.WriteString(c.renderInstr(instr))
			out.WriteByte(';')
		}
		out.WriteString(")->[")
		for i, s := range b.Succs {
			if i > 0 {
				out.WriteByte(',')
			}
			fmt.Fprintf(&out, "B%d", c.blockID(s))
		}
		out.WriteString("]")
		if b == fn.Recover {
			out.WriteString("(recover)")
		}
		out.WriteByte(' ')
	}
	return out.String()
}

// renderInstr renders one instruction: its result placeholder (if it
// defines a value), its kind, any non-Value fields that affect meaning
// (an operator, a struct field index, an asserted type...), and its
// Value-typed operands via Instruction.Operands — go/ssa's own complete,
// order-stable enumeration of every value an instruction references, so
// no instruction kind needs individually-listed operand fields here.
//
// *ssa.Phi is the one exception: it's rendered by renderPhi instead,
// because its Operands (Phi.Edges) are keyed to Block().Preds by
// position, an order that isn't otherwise canonicalized — see there.
func (c *ssaCanon) renderInstr(instr ssa.Instruction) string {
	if p, ok := instr.(*ssa.Phi); ok {
		return c.renderPhi(p)
	}

	var b strings.Builder
	v, isValue := instr.(ssa.Value)
	if isValue {
		b.WriteString(c.values[v])
		b.WriteByte('=')
	}
	fmt.Fprintf(&b, "%T", instr)
	b.WriteString(c.instrExtra(instr))
	b.WriteByte('(')
	for i, r := range instr.Operands(nil) {
		if i > 0 {
			b.WriteByte(',')
		}
		if r == nil || *r == nil {
			b.WriteString("nil")
		} else {
			b.WriteString(c.renderValue(*r))
		}
	}
	b.WriteByte(')')
	if isValue {
		b.WriteByte(':')
		b.WriteString(types.TypeString(v.Type(), c.qual))
	}
	return b.String()
}

// renderPhi renders a Phi node's incoming edges keyed to each
// predecessor's *canonical* block ID rather than its position in
// Block().Preds — Preds order reflects the order edges were added while
// building the CFG, an implementation detail that two structurally
// identical functions aren't guaranteed to share, so the edges are sorted
// by canonical predecessor ID before rendering.
func (c *ssaCanon) renderPhi(p *ssa.Phi) string {
	preds := p.Block().Preds
	type edge struct {
		pred int
		val  string
	}
	edges := make([]edge, len(p.Edges))
	for i, e := range p.Edges {
		val := "nil"
		if e != nil {
			val = c.renderValue(e)
		}
		edges[i] = edge{pred: c.blockID(preds[i]), val: val}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].pred < edges[j].pred })

	var b strings.Builder
	b.WriteString(c.values[p])
	b.WriteString("=Phi(")
	for i, e := range edges {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "B%d:%s", e.pred, e.val)
	}
	b.WriteString("):")
	b.WriteString(types.TypeString(p.Type(), c.qual))
	return b.String()
}

// instrExtra renders the non-Value fields that affect an instruction's
// meaning but aren't reachable via Operands — an operator, a struct field
// index, an asserted type, and so on. Comment fields (Alloc, Phi) are
// deliberately omitted: they're documentation hints, not semantics.
func (c *ssaCanon) instrExtra(instr ssa.Instruction) string {
	switch x := instr.(type) {
	case *ssa.BinOp:
		return fmt.Sprintf("{Op:%s}", x.Op)
	case *ssa.UnOp:
		return fmt.Sprintf("{Op:%s,CommaOk:%v}", x.Op, x.CommaOk)
	case *ssa.Lookup:
		return fmt.Sprintf("{CommaOk:%v}", x.CommaOk)
	case *ssa.TypeAssert:
		return fmt.Sprintf("{AssertedType:%s,CommaOk:%v}", types.TypeString(x.AssertedType, c.qual), x.CommaOk)
	case *ssa.FieldAddr:
		return fmt.Sprintf("{Field:%d}", x.Field)
	case *ssa.Field:
		return fmt.Sprintf("{Field:%d}", x.Field)
	case *ssa.Alloc:
		return fmt.Sprintf("{Heap:%v}", x.Heap)
	case *ssa.Next:
		return fmt.Sprintf("{IsString:%v}", x.IsString)
	case *ssa.Extract:
		return fmt.Sprintf("{Index:%d}", x.Index)
	case *ssa.Select:
		dirs := make([]string, len(x.States))
		for i, s := range x.States {
			dirs[i] = fmt.Sprintf("%d", s.Dir)
		}
		return fmt.Sprintf("{Blocking:%v,Dirs:%s}", x.Blocking, strings.Join(dirs, ","))
	case *ssa.Call:
		return c.callExtra(&x.Call)
	case *ssa.Go:
		return c.callExtra(&x.Call)
	case *ssa.Defer:
		return c.callExtra(&x.Call)
	default:
		return ""
	}
}

// callExtra renders the one part of a call that Operands doesn't cover:
// for an interface-method invocation (CallCommon.IsInvoke), which method.
// The receiver and arguments are ordinary Values, already covered by
// Operands.
func (c *ssaCanon) callExtra(cc *ssa.CallCommon) string {
	if cc.IsInvoke() {
		return "{Invoke:" + types.ObjectString(cc.Method, c.qual) + "}"
	}
	return ""
}

// renderValue renders one operand. A constant, a reference to a
// package-level function/variable, or a builtin must match exactly, so it
// prints as its literal identity; anything local — a parameter, a free
// variable, or the result of some other instruction — must already have a
// placeholder bound (renderFunc binds every one before rendering any
// instruction), so it prints as that placeholder.
func (c *ssaCanon) renderValue(v ssa.Value) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case *ssa.Const:
		if x.Value == nil {
			return "Const(" + types.TypeString(x.Type(), c.qual) + ":nil)"
		}
		return "Const(" + types.TypeString(x.Type(), c.qual) + ":" + x.Value.ExactString() + ")"
	case *ssa.Builtin:
		return "builtin:" + x.Name()
	case *ssa.Global:
		return "id:" + x.RelString(c.pass.Pkg)
	case *ssa.Function:
		return c.renderFunctionValue(x)
	default:
		if tok, ok := c.values[v]; ok {
			return tok
		}
		// Dominance guarantees every operand's defining instruction was
		// already visited while binding placeholders, so this should be
		// unreachable — bind it now rather than panic if it somehow isn't.
		return c.bindValue(v)
	}
}

// renderFunctionValue renders a reference to a *ssa.Function: a named
// function or method (anything with an Object — a symbol declared in
// source) must match exactly, by qualified name, same as any other
// non-local identifier. An anonymous function literal has no such
// identity to compare by, so its own control flow graph is compared
// instead, by recursing into renderFunc.
func (c *ssaCanon) renderFunctionValue(fn *ssa.Function) string {
	if obj := fn.Object(); obj != nil {
		return "id:" + fn.RelString(c.pass.Pkg)
	}
	return "closure(" + c.renderFunc(fn) + ")"
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

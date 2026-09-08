# duphelper

A `go/analysis` tool that flags top-level functions structurally identical
to another function already declared in the same package — the "we already
have a helper for this" pattern, caught mechanically instead of relying on
someone spotting it in review.

It was written after finding exactly this in review, twice in one session:
a test file needed a `func(float64) *float64` pointer-of helper, one already
existed in a sibling file in the same package, and a second, identically
bodied one got added under a different name — most likely to dodge a
"redeclared in this block" compile error, or simply because the existing one
wasn't found. `gofmt`/`go vet`/staticcheck don't catch this: nothing about
either declaration is individually wrong, the problem only exists in the
*relationship* between the two. See [`BLOG.html`](./BLOG.html) for the full
story — including the adversarial `clampLow`/`clampHigh` pair that forced
locals to be bound by declared position rather than order of first use, and
an interactive diagram of the analysis pipeline.

## What it flags

A top-level `func` (no receiver) that:

1. Has the same **signature by type** as another function in the package —
   parameter and result *names* don't matter (`ptrFloat64(v float64)
   *float64` and `ptrFloat64Widget(w float64) *float64` match), but every
   parameter/result *type*, the result count, and variadic-ness must.
2. Has an **alpha-equivalent body** — identical statement-for-statement and
   expression-for-expression once locals (parameters, named results, and
   anything the body itself declares) are replaced by a position-keyed
   placeholder instead of their literal name. Anything that *isn't* local —
   a package-level name, an import, a field/method selector, a literal's
   exact value — must still match exactly. Two functions with the same
   shape that read different globals, call different things, or return
   different constants are not duplicates.

Parameters and named results are keyed by their **declared position**, not
by the order they're first read in the body — see [`analyzer.go`](analyzer.go)'s
package doc for why that distinction matters (a naive first-use ordering
would wrongly match two functions that pick *different* operands out of the
same two parameters).

The second (and any later) declaration in a matching group is reported,
pointing back at the first — "first" meaning earliest by filename then line,
not by internal declaration order (see `testdata/demo`).

## What it doesn't do

- **Single-package only.** It can't tell you that you've reimplemented
  something that already exists as an *exported* helper in an imported
  package (e.g. a project's own `helpers` package) — only two declarations
  sitting in the same package are ever compared.
- **No methods.** Two methods with identical bodies can exist for a good
  reason (a matched pair implementing two different interfaces); comparing
  receiver types correctly is real added complexity this doesn't attempt
  yet.
- **No generics.** Functions with type parameters are never compared.
- Every finding is a candidate, not a verdict — a look-alike pair might
  still be intentional (see `clampLow`/`clampHigh` in `testdata/demo`, which
  duphelper correctly leaves alone despite looking similar at a glance).

## Usage

This package is just the analyzer — run it via the `slopless-go` binary in
[`cmd/slopless-go`](../cmd/slopless-go), which bundles every check in this
repo behind one command.

Without installing anything:

```bash
go run github.com/nerd-zero/slopless-go/cmd/slopless-go@latest duphelper ./...
```

Installed as a standalone command:

```bash
go install github.com/nerd-zero/slopless-go/cmd/slopless-go@latest
slopless-go duphelper ./...
```

As a `go vet` tool (this runs every bundled check, duphelper included — see
the repo root README for why):

```bash
go build -o /tmp/slopless-go github.com/nerd-zero/slopless-go/cmd/slopless-go
go vet -vettool=/tmp/slopless-go ./...
```

In your own project, wire it in as an advisory check, not a gate — see "What
it doesn't do" above. Two patterns that work well:

- **A pre-commit hook** that runs it only when a staged file is a `.go`
  file, prints findings, but always exits `0` so it never blocks the commit.
- **A CI step** with `continue-on-error: true` (or your CI's equivalent) —
  visible in the job log, doesn't fail the build.

## Configuration

None — there's nothing to tune. A match is either exact (by type, and
alpha-equivalent body) or it isn't; there's no threshold to raise or lower.

## Known limitations

- **No cross-package awareness.** The single most common real case this
  misses: a private reimplementation of something that already exists as an
  *exported* helper elsewhere in the module. Would need `analysis.Fact`
  export/import along the import graph to catch — not attempted yet.
- **Methods aren't compared**, only free functions. See "What it doesn't
  do" above.
- **Labels are compared by literal text, not alpha-renamed.** Two
  otherwise-identical functions using differently-named `goto` labels for
  the same control flow won't match — a missed finding, never a false one.
- **An empty-bodied function is never a candidate.** Matching stubs isn't
  an interesting finding.
- **Comments never affect the comparison** (deliberate, not a limitation in
  practice, but worth knowing) — two bodies that differ only in comments
  still match.

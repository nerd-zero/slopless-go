# singlecaller

A `go/analysis` tool that flags short, unexported functions and methods
called from exactly one site within their own package — usually a sign the
indirection isn't earning its keep and should just be inlined.

It was written after finding exactly this pattern in review: a row-to-model
builder copied from a convention used elsewhere in the same codebase, one
that only pays for itself when the underlying query generator produces
several distinct row types per entity. For the entity in question, every
query happened to return the same row type, so the "shared" builder had
exactly one caller and did nothing a plain struct literal wouldn't. See
[`BLOG.html`](./BLOG.html) for the full story (told through a small,
self-contained demo rather than the original code), including why no
existing linter catches this and what running the naive version taught us.

## What it flags

An unexported top-level `func` or method that:

1. Is **directly called** — `f(...)` or `x.f(...)` — from exactly one call
   expression in its package. A reference passed as a *value* (a handler
   registered with a router, a function handed to `go` as a value, etc.)
   does not count as a call, so those aren't flagged.
2. Has a **short body** (15 lines or fewer, `maxInlinableLines` in
   `main.go`). A one-caller function well over that is much more likely a
   deliberate readability split than a pointless wrapper.
3. Is called from a **call site that isn't already complex** (cyclomatic
   complexity 15 or under, `maxCallerComplexity` in `main.go`). Inlining a
   trivial helper into a function that already has a dozen branches doesn't
   remove complexity, it just relocates it — so a candidate whose one caller
   is already gnarly is left alone, on the assumption that whoever wrote
   that caller had their hands full without three more lines of a stranger's
   logic. The finding's message reports the call site's complexity, so this
   is visible even when it doesn't cross the ceiling.

It only reasons about unexported symbols. An exported func/method might
have callers in other packages a single-package pass can't see, so "one
use" isn't a safe signal there.

## What it doesn't do

It doesn't know *why* a function was split out. A short, single-caller
helper can still be the right call — a named security check, one half of a
matched encode/decode pair, an HTTP handler factory. Every finding is a
candidate, not a verdict; see the blog post for a worked example (a small,
self-contained demo, not excerpted from any real codebase) walking through
exactly this.

## Usage

This package is just the analyzer — run it via the `slopless-go` binary in
[`cmd/slopless-go`](../cmd/slopless-go), which bundles every check in this
repo behind one command.

Without installing anything:

```bash
go run github.com/nerd-zero/slopless-go/cmd/slopless-go@latest singlecaller ./...
```

Installed as a standalone command:

```bash
go install github.com/nerd-zero/slopless-go/cmd/slopless-go@latest
slopless-go singlecaller ./...
```

As a `go vet` tool (this runs every bundled check, singlecaller included —
see the repo root README for why):

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

Two knobs, both in `main.go`:

- `maxInlinableLines` (currently `15`) — the candidate's own body length.
  Lower it to be stricter about what counts as "short."
- `maxCallerComplexity` (currently `15`) — the call site's cyclomatic
  complexity ceiling. Lower it to stop recommending inlines into anything
  but the simplest callers; raise it if it's being filtered too eagerly.

## Known limitations

- **No sibling awareness.** A matched pair — an `encode`/`decode`, a
  `marshal`/`unmarshal` — can end up with only the shorter half clearing
  the line-count threshold, suggesting you inline one side of a pair that
  should stay symmetric. Worth a human glance, not worth chasing with more
  heuristics yet.
- **Self-recursion isn't excluded from the call count.** A function that
  only ever calls itself and is otherwise dead would be counted as "called
  once" (by itself) rather than caught as truly unused. `unused`
  (staticcheck) already covers genuinely dead code, so this hasn't come up
  in practice.
- **Line count is a proxy for "trivial," not a precise one.** It's a cheap
  signal, not a complexity metric — a dense 10-line function and an airy
  15-line one aren't equally "inlinable," and this tool doesn't try to
  tell them apart.
- **Cyclomatic complexity counts branches, not readability.** A caller with
  a long linear sequence of independent steps and no branching scores low
  even if it's genuinely hard to hold in your head, and a caller with a few
  early-return guard clauses scores higher than it "feels." It's a proxy
  for "already has a lot going on," not a judgment on the code.

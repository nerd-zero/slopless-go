# slopless-go

A small, growing collection of `go/analysis` tools that catch patterns
mainstream linters can't — because catching them requires knowing something
repo-specific: how your code generator works, what your team's conventions
actually imply, why a pattern that's normally fine stopped applying here.

Every tool here is deliberately **advisory, not a gate**. Findings are
candidates for a human to look at, not verdicts — see each tool's own
README for why.

## Install

One binary, every tool:

```bash
go install github.com/nerd-zero/slopless-go/cmd/slopless-go@latest
```

Or run it without installing anything:

```bash
go run github.com/nerd-zero/slopless-go/cmd/slopless-go@latest <tool> ./...
```

## Usage

```bash
slopless-go <tool> ./...     # run one check
slopless-go all ./...        # run every check
```

It also works as a single `go vet` tool covering every check at once — `go
vet -vettool=` always invokes one binary with flags, so there's no room for
a subcommand there; `slopless-go` detects that and runs everything:

```bash
go build -o /tmp/slopless-go ./cmd/slopless-go
go vet -vettool=/tmp/slopless-go ./...
```

## Tools

| Tool | Flags it as | Docs |
|---|---|---|
| [`singlecaller`](singlecaller) | A short, unexported func/method called from exactly one, uncomplicated call site — usually a sign a helper was copied from a pattern that stopped applying. | [README](singlecaller/README.md) · [the story behind it](singlecaller/BLOG.html) |

## Adding a new tool

Each tool is a self-contained top-level package:

```
newtool/
├── analyzer.go       # package newtool; var Analyzer = &analysis.Analyzer{...}
├── README.md         # what it flags, what it doesn't, how to configure it
└── testdata/
    └── demo/         # a small, runnable, made-up example — never real project code
```

Then register it in [`cmd/slopless-go/main.go`](cmd/slopless-go/main.go)'s
`registry` map. That's it — it's automatically available as
`slopless-go newtool`, folded into `slopless-go all`, and covered by
`go vet -vettool=`.

A few conventions worth keeping:

- **`testdata/` demos are real code, not fixtures.** Go tooling skips
  `testdata/` when expanding `./...`, so it never pollutes a real lint run
  of this repo — but write it as if someone will actually read it. It's
  the only example most users (and this repo's own README/blog posts) will
  ever see.
- **Advisory by default.** Don't build a tool that's confident enough to
  gate a build unless you're prepared to defend a low false-positive rate.
  When in doubt, report more context (like `singlecaller` reporting call
  site complexity) rather than a bare pass/fail.
- **No proprietary examples, ever.** Every demo, every doc example, is
  written from scratch for this repo. If a tool was inspired by something
  found in a real (private) codebase, describe the *pattern*, not the code.

## Repo layout

```
.
├── cmd/slopless-go/   # the single dispatcher binary — see its main.go doc comment
├── singlecaller/      # a tool: package + README + demo (the pattern every tool follows)
├── .githooks/         # optional local pre-commit hook (git config core.hooksPath .githooks)
└── .github/workflows/ # CI: build, vet, test, gofmt, then every tool as an advisory self-check
```

## Local development

```bash
git config core.hooksPath .githooks   # once, to enable the advisory pre-commit hook
go build ./...
go vet ./...
go test ./...
```

## Releasing

Versions are CalVer, `0.YYYYMM.MICRO` (same scheme as this org's other Go
projects) — the same month increments `MICRO`, a new month resets it to
`001`. Tags are the version prefixed with `v`.

```bash
./scripts/version.sh --dry-run    # preview the next version
./scripts/version.sh              # bump VERSION, commit, tag, push
```

## License

[MIT](LICENSE)

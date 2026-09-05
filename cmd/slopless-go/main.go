// Command slopless-go bundles every check in this repo behind one binary.
//
// Run one check by name:
//
//	slopless-go singlecaller ./...
//
// Run every check together:
//
//	slopless-go all ./...
//
// Or use it as a single go vet tool covering every check at once — go vet
// always invokes a -vettool as one binary with flags, so there's no room
// for a subcommand there. When the first argument looks like a flag rather
// than a tool name, slopless-go runs every check, same as "all":
//
//	go vet -vettool=$(command -v slopless-go) ./...
package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/nerd-zero/slopless-go/singlecaller"
)

// registry is every check this binary bundles. Add a new tool's Analyzer
// here to make it available as both "slopless-go <name>" and part of "all".
var registry = map[string]*analysis.Analyzer{
	"singlecaller": singlecaller.Analyzer,
}

func main() {
	// go vet -vettool= always passes flags first (e.g. -V=full during its
	// handshake) — there's no subcommand slot in that protocol, so treat
	// "first arg is a flag" as "run everything."
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "-") {
		multichecker.Main(allAnalyzers()...)
		return
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...) // drop the subcommand word before the tool parses its own flags

	if cmd == "all" {
		multichecker.Main(allAnalyzers()...)
		return
	}

	a, ok := registry[cmd]
	if !ok {
		usage()
		os.Exit(2)
	}
	singlechecker.Main(a)
}

func allAnalyzers() []*analysis.Analyzer {
	all := make([]*analysis.Analyzer, 0, len(registry))
	for _, a := range registry {
		all = append(all, a)
	}
	return all
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: slopless-go <tool> <packages...>")
	fmt.Fprintln(os.Stderr, "       slopless-go all <packages...>")
	fmt.Fprintln(os.Stderr, "\navailable tools:")
	for name := range registry {
		fmt.Fprintf(os.Stderr, "  %s\n", name)
	}
}

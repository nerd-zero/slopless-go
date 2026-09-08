// Package demo is a small, self-contained, entirely fictional example used
// by duphelper's README to demonstrate the tool's behavior without
// referencing any real codebase. It lives under testdata/, which Go tooling
// (go build, go vet, "./..." expansion) skips automatically, so it never
// shows up in a real lint run of this module.
package demo

// ptrFloat64 turns a value into a pointer to it — the usual shape for
// building an optional/nullable field in a test fixture.
func ptrFloat64(v float64) *float64 { return &v }

type Reading struct {
	Value *float64
}

func sampleReading() Reading {
	return Reading{Value: ptrFloat64(21.5)}
}

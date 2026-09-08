package demo

// ptrFloat64Widget has the same body as ptrFloat64 in pointers.go — most
// likely written without noticing that helper already existed two files
// over, or added under a new name to dodge a "ptrFloat64 redeclared"
// compile error. This is exactly what duphelper flags: same signature type
// (parameter/result names don't matter), same body once locals are
// alpha-renamed.
func ptrFloat64Widget(w float64) *float64 { return &w }

type Widget struct {
	Weight *float64
}

func sampleWidget() Widget {
	return Widget{Weight: ptrFloat64Widget(4.2)}
}

// clampLow and clampHigh look like a copy-paste pair at a glance — same
// shape, same parameter types — but they pick a *different* operand to
// return depending on the comparison, so they are genuinely different
// functions, not a duplicate. duphelper binds a and b to placeholders by
// their declared *position*, not by which one is read first in the body, so
// swapping which side of "<" wins is a real structural difference here, not
// a renaming that happens to look different.
func clampLow(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampHigh(a, b int) int {
	if a < b {
		return b
	}
	return a
}

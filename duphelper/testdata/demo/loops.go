package demo

// firstNegativeWhile returns the first negative element of nums, or -1 if
// there isn't one. Written as a "while" loop: the counter is declared
// before the loop and advanced at the end of the body.
func firstNegativeWhile(nums []int) int {
	i := 0
	for i < len(nums) {
		if nums[i] < 0 {
			return nums[i]
		}
		i++
	}
	return -1
}

// firstNegativeCountingFor does exactly what firstNegativeWhile does, but
// as a classic three-clause counting loop instead of a while loop. These
// two read as unrelated at the syntax level — different statement kinds,
// different position for the counter's declaration and increment — but
// they compile to the identical control flow graph, so duphelper's
// SSA-based comparison (see analyzer.go's package doc) catches this pair;
// the AST-based version this tool used before could not have. A change to
// the shared logic here (say, the negativity check) now only needs to
// happen in one of these, and duphelper will point at the other.
func firstNegativeCountingFor(nums []int) int {
	for i := 0; i < len(nums); i++ {
		if nums[i] < 0 {
			return nums[i]
		}
	}
	return -1
}

// timeoutSecondsHex and timeoutSecondsDecimal (below) demonstrate a
// second SSA-only capability: constants normalize. 0xA and 10 are
// different tokens in source but the same constant value once compiled,
// so a function differing from another only in how a literal was spelled
// now matches too.
func timeoutSecondsHex() int { return 0xA }

func timeoutSecondsDecimal() int { return 10 }

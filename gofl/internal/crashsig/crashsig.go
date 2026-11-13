// Package crashsig parses Go panic stacks into normalised signatures.
//
// Owner: TV2. Paper: C, section II-A (claims C1-C3 in the reading guide of
// phan_cong_fault_localization_v2.tex).
//
// Not tied to any application: input comes from "go test" output, process
// stderr or runtime/debug.Stack(). Frames of the panic machinery
// (runtime.gopanic, runtime.sigpanic, testing.tRunner, ...) must be dropped,
// otherwise every crash gets the same signature.
package crashsig

import "errors"

var ErrNotImplemented = errors.New("crashsig: not implemented yet")

// Frame is one stack frame. File is slash-separated.
type Frame struct {
	Func string
	File string
	Line int
}

// Signature is a normalised crash stack.
type Signature struct {
	Exception    string  // e.g. "runtime error: index out of range [3] with length 3"
	Frames       []Frame // crash point first
	NoCrashPoint bool    // true if no frame after panic/sigpanic was found
}

// Parse reads one panic stack.
func Parse(stack []byte) (Signature, error) {
	return Signature{}, ErrNotImplemented
}

// Normalize removes details that differ between runs of the same fault
// (level 2 grouping: goroutine ids, addresses, generated suffixes).
func Normalize(s Signature) Signature {
	return s
}

// Hash is a stable identity for a normalised signature.
func Hash(s Signature) string {
	return ""
}

// Subsumes reports whether a's frames contain b's frames (level 3 grouping,
// function names only, line numbers ignored).
//
// Decision (TV1, week 1, claim C3; see docs/CONTRACT.md section 5): the
// frames of b must appear in a as one CONTIGUOUS run, in the same order.
// Paper C only says "structurally contained" and compares fully qualified
// method names; it does not say whether gaps are allowed. Contiguous is the
// conservative reading: level 4 already merges groups sharing the crash-point
// file, so level 3 should not add false merges that pollute IBF. Revisit
// after the layer-2 measurement; this function is the only place to change.
// Both arguments are expected to be already Normalize()d.
func Subsumes(a, b Signature) bool {
	return false
}

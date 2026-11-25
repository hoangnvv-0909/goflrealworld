// Package diffprof compares two CPU profiles of the same workload at two
// sizes: Compute ranks the functions whose self time grew (hotspots.json) and
// Paths finds the call paths that lead to them (critical_paths.json). Both
// read profiles with package pprofread.
package diffprof

import "gofl/internal/contract"

// Error is a failure of this package that maps to a contract error code.
type Error struct {
	Code contract.Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// UsageError means the options were wrong (exit status 2 in the CLI).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Package crashrank groups crashes and ranks suspicious files and functions.
//
// Owner: TV2. Paper: C, sections II-A and II-B (claims C1-C7).
// The concrete IAD, IBF and FF formulas are NOT in the paper (claim C5):
// read CrashLocator (reference [21] of paper C) and document the formulas
// used here.
package crashrank

import (
	"errors"
	"time"

	"gofl/internal/crashsig"
	"gofl/internal/finding"
)

var ErrNotImplemented = errors.New("crashrank: not implemented yet")

// Event is one observed crash.
type Event struct {
	Time      time.Time
	Signature crashsig.Signature
	Source    string // e.g. "go test", "stderr", log file name
}

// Group is a set of crashes considered the same underlying fault.
type Group struct {
	ID     string
	Level  int // 1..4, the grouping level that merged them
	Events []Event
}

// GroupEvents applies the four grouping levels (C1-C4).
func GroupEvents(events []Event) ([]Group, error) {
	return nil, ErrNotImplemented
}

// Rank scores files of one group by IAD * IBF * FF against all groups (C5, C6)
// and suggests functions by frequency inside the top files (C7).
func Rank(g Group, all []Group) ([]finding.Finding, error) {
	return nil, ErrNotImplemented
}

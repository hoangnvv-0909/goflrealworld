// Package finding holds the result type that every gofl command produces.
//
// Owner: TV1. This is part of the interface contract: changes need a PR
// reviewed by TV1 and announced to both groups (plan, section "Nguyen tac").
package finding

// SchemaVersion is bumped on any incompatible change to the JSON shape.
// The VS Code extension (TV7) checks it.
const SchemaVersion = 1

// Location is a position in the analysed project. File is slash-separated
// and relative to the project root, so output is identical on every OS.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
	Func string `json:"func,omitempty"`
}

// Evidence explains why a location is suspicious.
// Kind is a short machine tag, e.g. "stack-rank", "crash-group", "delta-self",
// "ndc", "llm"; Detail is human-readable.
type Evidence struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Finding is one suspicious location.
type Finding struct {
	Location Location   `json:"location"`
	Score    float64    `json:"score"` // comparable only within one run
	Rank     int        `json:"rank"`  // 1 = most suspicious
	Evidence []Evidence `json:"evidence,omitempty"`
	Flow     []Location `json:"flow,omitempty"` // optional execution path, maps to SARIF codeFlows
}

// Report is the top-level JSON document written by "gofl <cmd> -format json".
type Report struct {
	Version  int       `json:"version"`
	Command  string    `json:"command"` // "crash" or "perf"
	Findings []Finding `json:"findings"`
	// Notes carries limits the user must know about, e.g.
	// "CPU profile does not see I/O wait" or why tests could not run.
	Notes []string `json:"notes,omitempty"`
}

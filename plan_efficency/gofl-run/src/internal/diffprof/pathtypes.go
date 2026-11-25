package diffprof

import "gofl/internal/contract"

// PathFrame is one frame of a path (contract v0, section 3.3). Name is the
// short name package.Function.
type PathFrame struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int64  `json:"line"`
}

// CriticalPath is one entry of critical_paths.json (section 3.3).
type CriticalPath struct {
	Target     string      `json:"target"`
	TargetRank int         `json:"target_rank"`
	Signature  string      `json:"signature"`
	Score      float64     `json:"score"`
	Truncated  bool        `json:"truncated"`
	Frames     []PathFrame `json:"frames"`
}

// Methods of extracting paths (run.json: paths_method).
const (
	MethodPaper = "paper" // method 1, as in the paper
	MethodStack = "stack" // method 2, the fallback
	MethodAuto  = "auto"  // method 1; on ENTRY_NOT_COVERING, method 2
)

// CodeEntryNotCovering is the contract code Paths returns when the entry
// point does not cover every occurrence of a hotspot.
const CodeEntryNotCovering = contract.EntryNotCovering

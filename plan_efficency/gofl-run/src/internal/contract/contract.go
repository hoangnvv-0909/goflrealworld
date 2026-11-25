// Package contract holds the two files this part of gofl writes, run.json
// (section 3.1 of data contract v0) and diagnostics.json (section 3.4), and
// the error codes of the contract.
package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ToolVersion is the version written into run.json and diagnostics.json.
const ToolVersion = "0.1.0"

// File names inside the output directory.
const (
	RunFile         = "run.json"
	DiagnosticsFile = "diagnostics.json"
	BaseProfile     = "base.pprof"
	ScaledProfile   = "scaled.pprof"
	HotspotsFile    = "hotspots.json"       // written by diffprof.Compute
	PathsFile       = "critical_paths.json" // written by diffprof.Paths
)

// Values of the status, mode and paths_method fields.
const (
	StatusOK    = "ok"
	StatusError = "error"
	ModePprof   = "pprof"
	ModeBench   = "bench"
	PathsPaper  = "paper"
	PathsStack  = "stack"
)

// Code is an error code of contract v0.
type Code string

// The error codes, in the order the contract lists them.
const (
	NoGo             Code = "NO_GO"              // Go is missing or unusable
	NoModule         Code = "NO_MODULE"          // no go.mod, or dependencies cannot be resolved
	NoBench          Code = "NO_BENCH"           // benchmark or size not found, or no result
	BuildFail        Code = "BUILD_FAIL"         // the test binary does not compile
	TestNeedsEnv     Code = "TEST_NEEDS_ENV"     // setup failed, or the benchmark failed or skipped itself
	Timeout          Code = "TIMEOUT"            // a setup command or a benchmark run took too long
	NoProfile        Code = "NO_PROFILE"         // a profile is missing, unreadable or empty
	NoPositiveDelta  Code = "NO_POSITIVE_DELTA"  // diffprof.Compute found no function with Δ > 0
	EntryNotCovering Code = "ENTRY_NOT_COVERING" // diffprof.Paths: a hotspot lies outside the entry
)

// Codes lists every error code of contract v0.
var Codes = []Code{NoGo, NoModule, NoBench, BuildFail, TestNeedsEnv, Timeout, NoProfile, NoPositiveDelta, EntryNotCovering}

// Valid reports whether c is one of Codes.
func (c Code) Valid() bool {
	for _, k := range Codes {
		if c == k {
			return true
		}
	}
	return false
}

// Run is run.json.
type Run struct {
	ToolVersion string  `json:"tool_version"`
	Status      string  `json:"status"`
	ErrorCode   *Code   `json:"error_code"`
	Mode        string  `json:"mode"`
	GoVersion   *string `json:"go_version"`
	OS          string  `json:"os"`
	DurationMs  int64   `json:"duration_ms"`
	BasePprof   *string `json:"base_pprof"`
	ScaledPprof *string `json:"scaled_pprof"`
	PathsMethod string  `json:"paths_method"`

	// Bench is not part of contract v0: it is a proposed extension that
	// records what the benchmark mode measured, so diffprof.Compute can
	// compare time per operation. Readers that follow v0 ignore it.
	Bench *Bench `json:"bench,omitempty"`
}

// Bench describes the two benchmark runs. Iterations is the sum of b.N
// over the results of a run; NsPerOp is their weighted mean.
type Bench struct {
	Sizes            [2]string `json:"sizes"`
	BenchTime        string    `json:"benchtime"`
	BaseIterations   int64     `json:"base_iterations"`
	ScaledIterations int64     `json:"scaled_iterations"`
	BaseNsPerOp      float64   `json:"base_ns_per_op"`
	ScaledNsPerOp    float64   `json:"scaled_ns_per_op"`
}

// Fail marks r as failed with code c.
func (r *Run) Fail(c Code) {
	r.Status = StatusError
	r.ErrorCode = &c
}

// Diagnostics is diagnostics.json. It holds no function or file names, so it
// can always be sent. Counts the run did not reach are null.
type Diagnostics struct {
	ToolVersion          string  `json:"tool_version"`
	Status               string  `json:"status"`
	ErrorCode            *Code   `json:"error_code"`
	Mode                 string  `json:"mode"`
	GoVersion            *string `json:"go_version"`
	OS                   string  `json:"os"`
	DurationMs           int64   `json:"duration_ms"`
	BaselineSamplesTotal *int64  `json:"baseline_samples_total"`
	ScaledSamplesTotal   *int64  `json:"scaled_samples_total"`
	HotspotCount         *int    `json:"hotspot_count"`
	PathCount            *int    `json:"path_count"`
}

// Ptr returns a pointer to v, for the nullable fields.
func Ptr[T any](v T) *T { return &v }

// WriteJSON writes v to path as indented JSON. It writes a temporary file
// first so a reader never sees half a file.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ReadRun reads run.json.
func ReadRun(path string) (*Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if r.ErrorCode != nil && !r.ErrorCode.Valid() {
		return nil, fmt.Errorf("%s: unknown error_code %q", path, *r.ErrorCode)
	}
	return &r, nil
}

// PathFor turns a file gofl wrote into the form run.json stores: relative to
// the directory gofl runs in (the project root for the team leader), with
// forward slashes. A file outside that directory stays absolute.
func PathFor(file string) string {
	abs, err := filepath.Abs(file)
	if err != nil {
		return filepath.ToSlash(file)
	}
	wd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(abs)
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// Resolve finds a path stored in run.json. Paths are relative to the
// directory gofl ran in; if the reader runs elsewhere, the path is also
// tried relative to the directory that holds run.json and its parent.
func Resolve(runPath, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	dir := filepath.Dir(runPath)
	for _, base := range []string{dir, filepath.Dir(dir)} {
		q := filepath.Join(base, p)
		if _, err := os.Stat(q); err == nil {
			return q
		}
	}
	return p
}

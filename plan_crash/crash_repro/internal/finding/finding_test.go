package finding

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// The example file is what the VS Code extension (TV7) develops against.
// This test keeps it in sync with the Go types.
func TestExampleMatchesSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/example.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r Report
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("example.json does not match Report: %v", err)
	}
	if r.Version != SchemaVersion {
		t.Fatalf("example version %d, want %d", r.Version, SchemaVersion)
	}
	for i, f := range r.Findings {
		if f.Rank != i+1 {
			t.Errorf("finding %d has rank %d, want %d", i, f.Rank, i+1)
		}
	}
}

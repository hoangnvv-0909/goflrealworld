package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gofl/internal/finding"
)

var sample = finding.Report{
	Command: "crash",
	Findings: []finding.Finding{{
		Location: finding.Location{File: "crash/crash.go", Line: 16, Func: "EmailOf"},
		Score:    1, Rank: 1,
		Evidence: []finding.Evidence{{Kind: "stack-rank", Detail: "top frame after panic"}},
	}},
}

func TestJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, JSON, sample); err != nil {
		t.Fatal(err)
	}
	var got finding.Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != finding.SchemaVersion || got.Findings[0].Location.Line != 16 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestMarkdown(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Markdown, sample); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "`crash/crash.go:16`") {
		t.Fatalf("missing location:\n%s", buf.String())
	}
}

func TestSARIFNotYet(t *testing.T) {
	if err := Write(&bytes.Buffer{}, SARIF, sample); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("got %v", err)
	}
}

func TestParseFormat(t *testing.T) {
	if _, err := ParseFormat("xml"); err == nil {
		t.Fatal("want error")
	}
	if f, _ := ParseFormat("MD"); f != Markdown {
		t.Fatal(f)
	}
}

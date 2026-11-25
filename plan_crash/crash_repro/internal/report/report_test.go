package report

import (
	"bytes"
	"encoding/json"
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

func TestSARIF(t *testing.T) {
	r := finding.Report{
		Command: "crash",
		Findings: []finding.Finding{
			{Location: finding.Location{File: "crash/crash.go", Line: 16, Func: "crash.EmailOf"}, Score: 2, Rank: 1,
				Evidence: []finding.Evidence{{Kind: "stack-rank", Detail: "x"}},
				Flow:     []finding.Location{{File: "main.go", Line: 3}, {File: "crash/crash.go", Line: 16}}},
			{Location: finding.Location{File: "main.go"}, Score: 1, Rank: 2},
		},
		Notes: []string{"limit one"},
	}
	var buf bytes.Buffer
	if err := Write(&buf, SARIF, r); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version string `json:"version"`
		Runs    []struct {
			Invocations []struct {
				Notes []struct {
					Message struct{ Text string } `json:"message"`
				} `json:"toolExecutionNotifications"`
			} `json:"invocations"`
			Results []struct {
				Rank      float64 `json:"rank"`
				Locations []struct {
					Physical struct {
						Artifact struct{ URI, URIBaseID string } `json:"artifactLocation"`
						Region   *struct{ StartLine int }        `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				CodeFlows []struct {
					ThreadFlows []struct {
						Locations []json.RawMessage `json:"locations"`
					} `json:"threadFlows"`
				} `json:"codeFlows"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "2.1.0" || len(got.Runs) != 1 || len(got.Runs[0].Results) != 2 {
		t.Fatalf("bad document:\n%s", buf.String())
	}
	res := got.Runs[0].Results
	if res[0].Rank != 100 || res[1].Rank != 50 {
		t.Errorf("ranks = %v, %v", res[0].Rank, res[1].Rank)
	}
	loc := res[0].Locations[0].Physical
	if loc.Artifact.URI != "crash/crash.go" || loc.Artifact.URIBaseID != "%SRCROOT%" || loc.Region == nil || loc.Region.StartLine != 16 {
		t.Errorf("location = %+v", loc)
	}
	if res[1].Locations[0].Physical.Region != nil {
		t.Error("line 0 must not produce a region")
	}
	if len(res[0].CodeFlows) != 1 || len(res[0].CodeFlows[0].ThreadFlows[0].Locations) != 2 {
		t.Errorf("codeFlows = %+v", res[0].CodeFlows)
	}
	if n := got.Runs[0].Invocations[0].Notes; len(n) != 1 || n[0].Message.Text != "limit one" {
		t.Errorf("notes = %+v", n)
	}
	// empty report is still valid
	buf.Reset()
	if err := Write(&buf, SARIF, finding.Report{Command: "crash"}); err != nil || !strings.Contains(buf.String(), `"results": []`) {
		t.Errorf("empty report: %v\n%s", err, buf.String())
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

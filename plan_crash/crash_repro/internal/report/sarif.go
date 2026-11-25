package report

import (
	"encoding/json"
	"io"
	"strings"

	"gofl/internal/finding"
)

// SARIF 2.1.0 rendering (docs/CONTRACT.md section 4): one result per Finding;
// rank is Score scaled to 0-100 against the best score of the report;
// locations[0] is the Location; codeFlows[0].threadFlows[0].locations is Flow;
// the message is the joined Evidence; Notes become
// invocations[0].toolExecutionNotifications. File paths are relative to the
// project root, so they use uriBaseId %SRCROOT%.

const (
	sarifSchema  = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifVersion = "2.1.0"
	srcRoot      = "%SRCROOT%"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifInvocation struct {
	ExecutionSuccessful        bool                `json:"executionSuccessful"`
	ToolExecutionNotifications []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Rank      float64         `json:"rank"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
	CodeFlows []sarifCodeFlow `json:"codeFlows,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical  `json:"physicalLocation"`
	LogicalLocations []sarifLogical `json:"logicalLocations,omitempty"`
	Message          *sarifMessage  `json:"message,omitempty"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}

type sarifLogical struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type sarifCodeFlow struct {
	ThreadFlows []sarifThreadFlow `json:"threadFlows"`
}

type sarifThreadFlow struct {
	Locations []sarifThreadFlowLocation `json:"locations"`
}

type sarifThreadFlowLocation struct {
	Location sarifLocation `json:"location"`
}

func toSarifLocation(l finding.Location) sarifLocation {
	p := sarifPhysical{ArtifactLocation: sarifArtifact{URI: l.File, URIBaseID: srcRoot}}
	if l.Line > 0 {
		p.Region = &sarifRegion{StartLine: l.Line, StartColumn: l.Col}
	}
	out := sarifLocation{PhysicalLocation: p}
	if l.Func != "" {
		out.LogicalLocations = []sarifLogical{{Name: l.Func, Kind: "function"}}
	}
	return out
}

func writeSARIF(w io.Writer, r finding.Report) error {
	rule := sarifRule{
		ID:               "gofl/" + r.Command,
		ShortDescription: sarifMessage{Text: "Suspicious location ranked by gofl " + r.Command},
	}
	best := 0.0
	for _, f := range r.Findings {
		if f.Score > best {
			best = f.Score
		}
	}
	results := make([]sarifResult, 0, len(r.Findings))
	for _, f := range r.Findings {
		rank := 0.0
		if best > 0 {
			rank = f.Score / best * 100
		}
		parts := make([]string, len(f.Evidence))
		for i, e := range f.Evidence {
			parts[i] = e.Kind + ": " + e.Detail
		}
		msg := "Suspicious location"
		if f.Location.Func != "" {
			msg += " in " + f.Location.Func
		}
		if len(parts) > 0 {
			msg += ". " + strings.Join(parts, "; ")
		}
		res := sarifResult{
			RuleID:    rule.ID,
			Level:     "warning",
			Rank:      rank,
			Message:   sarifMessage{Text: msg},
			Locations: []sarifLocation{toSarifLocation(f.Location)},
		}
		if len(f.Flow) > 0 {
			tf := sarifThreadFlow{}
			for _, l := range f.Flow {
				tf.Locations = append(tf.Locations, sarifThreadFlowLocation{Location: toSarifLocation(l)})
			}
			res.CodeFlows = []sarifCodeFlow{{ThreadFlows: []sarifThreadFlow{tf}}}
		}
		results = append(results, res)
	}
	inv := sarifInvocation{ExecutionSuccessful: true}
	for _, n := range r.Notes {
		inv.ToolExecutionNotifications = append(inv.ToolExecutionNotifications,
			sarifNotification{Level: "note", Message: sarifMessage{Text: n}})
	}
	log := sarifLog{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs: []sarifRun{{
			Tool:        sarifTool{Driver: sarifDriver{Name: "gofl", Rules: []sarifRule{rule}}},
			Invocations: []sarifInvocation{inv},
			Results:     results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

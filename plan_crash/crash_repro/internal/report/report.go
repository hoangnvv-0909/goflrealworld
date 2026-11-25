// Package report writes findings as JSON, Markdown or SARIF.
//
// Owner: TV1. SARIF 2.1.0 support is to be verified by TV7 in week 1
// before it is implemented (plan, week 1 day 1-3).
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gofl/internal/finding"
)

type Format string

const (
	JSON     Format = "json"
	Markdown Format = "md"
	SARIF    Format = "sarif"
)

var ErrNotImplemented = errors.New("not implemented yet")

// ParseFormat accepts the values of the -format flag.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(s)); f {
	case JSON, Markdown, SARIF:
		return f, nil
	}
	return "", fmt.Errorf("unknown format %q (want json, md or sarif)", s)
}

// Write renders r in format f.
func Write(w io.Writer, f Format, r finding.Report) error {
	if r.Version == 0 {
		r.Version = finding.SchemaVersion
	}
	switch f {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case Markdown:
		return writeMarkdown(w, r)
	case SARIF:
		return writeSARIF(w, r)
	}
	return fmt.Errorf("unknown format %q", f)
}

func writeMarkdown(w io.Writer, r finding.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# gofl %s\n\n", r.Command)
	if len(r.Findings) == 0 {
		b.WriteString("No suspicious locations found.\n")
	} else {
		b.WriteString("| Rank | Location | Function | Score | Evidence |\n")
		b.WriteString("|---:|---|---|---:|---|\n")
		for _, f := range r.Findings {
			loc := f.Location.File
			if f.Location.Line > 0 {
				loc = fmt.Sprintf("%s:%d", loc, f.Location.Line)
			}
			ev := make([]string, len(f.Evidence))
			for i, e := range f.Evidence {
				ev[i] = e.Kind + ": " + e.Detail
			}
			fmt.Fprintf(&b, "| %d | `%s` | `%s` | %.3f | %s |\n",
				f.Rank, loc, f.Location.Func, f.Score, strings.Join(ev, "; "))
		}
	}
	if len(r.Notes) > 0 {
		b.WriteString("\n**Notes**\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

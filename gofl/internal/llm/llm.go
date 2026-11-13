// Package llm is the optional LLM triage layer for "gofl crash".
//
// Owner: TV3. Paper: C, sections II-C and IV-B (claims C8-C13).
// Off by default; the API key is supplied by the user. Redact is NOT in
// any paper: it is the team's addition. Before sending, gofl must show the
// user exactly what will leave the machine.
package llm

import (
	"context"
	"errors"

	"gofl/internal/finding"
)

var ErrNotImplemented = errors.New("llm: not implemented yet")

// Provider is one model backend (hosted API or local model).
type Provider interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// Input is what the prompt is built from.
type Input struct {
	Exception string
	Stack     string
	Ranked    []finding.Finding // output of crashrank
	Files     map[string]string // path -> source, only files the user allowed
}

// Result is the parsed model answer.
type Result struct {
	Locations   []finding.Location
	Explanation string
}

// Redact removes secrets and personal data before anything is sent.
func Redact(s string) string {
	return s
}

// BuildPrompt builds the five-part zero-shot prompt (C8, Listing 1).
func BuildPrompt(in Input) (string, error) {
	return "", ErrNotImplemented
}

// ParseResponse parses a closed-schema JSON answer, tolerating text outside
// the code block and truncated output (C13).
func ParseResponse(raw []byte) (Result, error) {
	return Result{}, ErrNotImplemented
}

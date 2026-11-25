// Package llm is the optional LLM triage layer for "gofl crash".
//
// Owner: TV3. Paper: C, sections II-C and IV-B (claims C8-C13).
// Off by default; the API key is supplied by the user. Redact is NOT in
// any paper: it is the team's addition. Before sending, gofl must show the
// user exactly what will leave the machine (see DryRun).
//
// Files of the package:
//
//	llm.go       types shared by the files below
//	provider.go  Provider implementations (OpenAI-compatible HTTP, fake)
//	redact.go    Redact
//	stack.go     light stack reader used to pick files for the prompt
//	prompt.go    BuildPrompt, five-part zero-shot prompt (C8, Listing 1)
//	parse.go     ParseResponse, tolerant of text around JSON / truncation (C13)
//	vote.go      Triage: five independent runs, majority rule (C11, C12)
//	eval.go      scoring helpers used by cmd/llmeval (Acc@k, 3-of-5)
//
// Design decisions (TV3, week 1):
//   - The paper's Listing 1 asks the model for PATCHED CODE. gofl only
//     localises, so part five asks for locations as closed-schema JSON.
//     Numbers of the paper therefore cannot be compared with ours directly.
//   - Configuration (stack trace vs. stack message, with or without the
//     ranking) follows the four prompt combinations of the paper (PromptConfig).
//   - Secrets come from the environment only (GOFL_LLM_*), never from flags
//     or files in the repository.
package llm

import (
	"context"
	"errors"

	"gofl/internal/finding"
)

// ErrNotImplemented is kept for source compatibility with the skeleton.
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

	// Fields below are additions of TV3 (readers ignore them safely).

	// Tier says how the answer was recovered: 0 none, 1 plain JSON,
	// 2 fenced block, 3 balanced braces, 4 truncated JSON, 5 JSON after
	// removing trailing commas, 6 free text fallback (least reliable).
	Tier int
	// Ungrounded are locations the model named that are not among the files
	// the prompt offered (probable invention). They are not in Locations.
	Ungrounded []finding.Location
}

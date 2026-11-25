// Package agentprompt builds the prompt that a repair Agent receives for one
// crash group (plan_crash/KE_HOACH_BAI_C.md, step 10).
//
// The Agent sees the whole project, so the prompt carries no source code. It
// has two modes that share one template:
//
//	E0  crash facts only: exception, stack, how to reproduce
//	E1  E0 plus the top 3 files of crashrank.Rank (3 functions each)
//
// E0 differs from E1 only by the ranking section, so the gap between the two
// measures exactly what the top 3 is worth. The text is deterministic (no model
// writes it), so a run can be reproduced.
package agentprompt

import (
	"errors"
	"fmt"
	"strings"

	"gofl/internal/crashrank"
	"gofl/internal/crashsig"
	"gofl/internal/finding"
	"gofl/internal/llm"
)

// What the Agent is shown from the ranking.
const (
	TopFiles     = 3 // files, as in paper C's prompt
	FuncsPerFile = 3
	// LocalizeFile is where the Agent writes its answer, relative to the
	// project root.
	LocalizeFile = "gofl-localize.json"
	// maxExtraFrames caps the "other traces" lines.
	maxExtraFrames = 8
)

// Mode selects what the prompt contains.
type Mode int

const (
	E0 Mode = iota // no ranking
	E1             // top 3 files of the ranking
)

func (m Mode) String() string {
	if m == E1 {
		return "E1"
	}
	return "E0"
}

// FuncHint is one suggested function.
type FuncHint struct {
	Func  string  `json:"func"`
	Line  int     `json:"line,omitempty"`
	Score float64 `json:"score"`
}

// FileHint is one of the top files with its best functions.
type FileHint struct {
	File  string     `json:"file"`
	Score float64    `json:"score"` // score of the file (that of its best function)
	Funcs []FuncHint `json:"funcs"`
}

// Hints reduces a function-level ranking to its first nFiles files, each with
// at most nFuncs functions. crashrank.Rank orders findings by score and the
// first finding of a file carries the file's score, so files come out in file
// order and functions in the order Rank gave them.
func Hints(ranked []finding.Finding, nFiles, nFuncs int) []FileHint {
	var out []FileHint
	at := map[string]int{}
	for _, f := range ranked {
		i, ok := at[f.Location.File]
		if !ok {
			if len(out) >= nFiles {
				continue
			}
			i = len(out)
			at[f.Location.File] = i
			out = append(out, FileHint{File: f.Location.File, Score: f.Score})
		}
		if len(out[i].Funcs) < nFuncs {
			out[i].Funcs = append(out[i].Funcs, FuncHint{Func: f.Location.Func, Line: f.Location.Line, Score: f.Score})
		}
	}
	return out
}

// Input is what the prompt is built from.
type Input struct {
	Group  crashrank.Group
	Ranked []finding.Finding // crashrank.Rank of Group; only used in E1
	Repro  []string          // commands that reproduce the crash, one per entry point
	// MustPass says which tests must still pass; empty means "every other test".
	MustPass string
}

// Build renders the prompt. It fails when the group has no event.
func Build(in Input, m Mode) (string, error) {
	if len(in.Group.Events) == 0 {
		return "", errors.New("agentprompt: empty crash group")
	}
	first := in.Group.Events[0].Signature
	var b strings.Builder

	b.WriteString("You are fixing a crash in a Go project. The whole project is in the current directory.\n\n")

	b.WriteString("## 1. The crash\n\n")
	fmt.Fprintf(&b, "- Exception: %s\n", llm.Redact(orNone(first.Exception)))
	fmt.Fprintf(&b, "- Crash events in this group: %d (merged at grouping level %d of 4)\n", len(in.Group.Events), in.Group.Level)
	if len(in.Repro) == 0 {
		b.WriteString("- Reproduce: no command is known; find a test or input that triggers it.\n")
	} else {
		b.WriteString("- Reproduce (one command per entry point; every one must stop crashing):\n")
		for _, c := range in.Repro {
			fmt.Fprintf(&b, "  - `%s`\n", c)
		}
	}

	b.WriteString("\n## 2. Stack\n\nRepresentative trace:\n\n```\n")
	b.WriteString(strings.TrimRight(llm.Redact(first.Raw), "\n"))
	b.WriteString("\n```\n")
	if extra := otherFrames(in.Group); len(extra) > 0 {
		b.WriteString("\nOther traces of the group also pass through these frames, which the trace above does not show:\n\n")
		for _, l := range extra {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}

	if m == E1 {
		b.WriteString("\n## 3. Where gofl ranks the suspicion\n\n")
		b.WriteString("gofl ranked the files on the crash stacks by IAD x IBF x FF (distance to the crash point, rarity across groups, share of traces). Top files, best first:\n\n")
		hints := Hints(in.Ranked, TopFiles, FuncsPerFile)
		if len(hints) == 0 {
			b.WriteString("(no ranking available for this crash)\n")
		}
		for i, h := range hints {
			fmt.Fprintf(&b, "%d. `%s` (score %.3f)\n", i+1, h.File, h.Score)
			for _, f := range h.Funcs {
				fmt.Fprintf(&b, "   - `%s`%s\n", f.Func, atLine(f.Line))
			}
		}
		b.WriteString("\nTreat this as a hint, not as the answer. The ranking only follows how the crash propagated. " +
			"The bad value (nil, index, length, capacity, ...) may have been created in a function that had already " +
			"returned before the crash, so trace the value back to where it comes from. The root cause can be outside these files.\n")
	}

	n := 3 // section number: the ranking section is 3 in E1
	if m == E1 {
		n = 4
	}
	fmt.Fprintf(&b, "\n## %d. What to do, in this order\n\n", n)
	b.WriteString("1. Reproduce the crash with the command(s) above.\n")
	fmt.Fprintf(&b, "2. Locate the root cause. Before you change any code, write `%s` in the project root:\n\n", LocalizeFile)
	b.WriteString("   ```json\n   {\"locations\": [{\"file\": \"path/relative/to/root.go\", \"function\": \"Type.Method\", \"line\": 0}], \"explanation\": \"...\"}\n   ```\n\n")
	b.WriteString("   List the places to fix, most important first.\n")
	b.WriteString("3. Fix the root cause. Do not only add a nil check, a bounds check or a recover at the crash point if the fault is elsewhere.\n")
	mp := in.MustPass
	if mp == "" {
		mp = "every other test of the project"
	}
	fmt.Fprintf(&b, "4. Run the reproduce command(s) again: they must no longer crash. %s must still pass.\n", capFirst(mp))
	return b.String(), nil
}

// otherFrames lists "func (file:line)" for frames that appear in some event of
// the group but not in the first one, in order of first appearance.
func otherFrames(g crashrank.Group) []string {
	key := func(f crashsig.Frame) string { return f.Func + "|" + f.File }
	seen := map[string]bool{}
	for _, f := range crashsig.Normalize(g.Events[0].Signature).Frames {
		seen[key(f)] = true
	}
	var out []string
	for _, e := range g.Events[1:] {
		for _, f := range crashsig.Normalize(e.Signature).Frames {
			if seen[key(f)] || f.File == "" || crashsig.IsStdlibFile(f.File) {
				continue
			}
			seen[key(f)] = true
			out = append(out, fmt.Sprintf("`%s` (%s:%d)", f.Func, f.File, f.Line))
			if len(out) >= maxExtraFrames {
				return out
			}
		}
	}
	return out
}

func atLine(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(" (line %d)", n)
}

func orNone(s string) string {
	if s == "" {
		return "(none printed)"
	}
	return s
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gofl/internal/finding"
)

// DefaultRuns is the number of independent requests per case (C11).
const DefaultRuns = 5

// Quorum is the number of correct answers that makes a case "solved":
// a majority, 3 of 5 in paper C (C12).
func Quorum(runs int) int { return runs/2 + 1 }

// Outcome is everything Triage learned about one case.
type Outcome struct {
	Prompt   string            // exactly what was sent (after Redact)
	Raw      []string          // raw answers, "" for failed requests
	Results  []Result          // parsed answers of the requests that gave one
	Findings []finding.Finding // voted locations, most voted first
	Failed   int               // requests that failed or could not be parsed
	Notes    []string          // limits the user must know about
	Errs     []error           // one per failed request (for logs, not for users)
}

// TriageOptions are the knobs of Triage; the zero value means defaults.
type TriageOptions struct {
	Config      PromptConfig
	Runs        int // default DefaultRuns
	Concurrency int // parallel requests, default 1
}

// DryRun returns the exact text Triage would send, without sending anything.
// gofl prints it so the user sees what leaves the machine.
func DryRun(in Input, cfg PromptConfig) (string, error) {
	return BuildPromptWith(in, cfg)
}

// Triage asks the provider opts.Runs independent times (each a fresh
// conversation) and merges the answers by vote. It fails only when no answer
// at all could be used; partial failures are counted in Outcome.Failed and
// described in Outcome.Notes.
func Triage(ctx context.Context, p Provider, in Input, opts TriageOptions) (Outcome, error) {
	if opts.Runs <= 0 {
		opts.Runs = DefaultRuns
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Config == (PromptConfig{}) {
		opts.Config = DefaultConfig()
	}
	prompt, err := BuildPromptWith(in, opts.Config)
	if err != nil {
		return Outcome{}, err
	}
	hint := HintFor(in)

	out := Outcome{Prompt: prompt, Raw: make([]string, opts.Runs)}
	results := make([]*Result, opts.Runs)
	errs := make([]error, opts.Runs)

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	for i := 0; i < opts.Runs; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ans, err := p.Complete(ctx, prompt)
			out.Raw[i] = ans
			if err != nil {
				errs[i] = err
				return
			}
			res, err := ParseResponseWith([]byte(ans), hint)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = &res
		}(i)
	}
	wg.Wait()

	for i := 0; i < opts.Runs; i++ {
		if results[i] != nil {
			out.Results = append(out.Results, *results[i])
		} else {
			out.Failed++
			out.Errs = append(out.Errs, errs[i])
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if len(out.Results) == 0 {
		return out, fmt.Errorf("llm: none of %d requests gave a usable answer: %w", opts.Runs, firstErr(errs))
	}
	out.Findings = Aggregate(out.Results, opts.Runs)
	out.Notes = notes(out, opts)
	return out, nil
}

func firstErr(errs []error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return errors.New("unknown error")
}

func notes(o Outcome, opts TriageOptions) []string {
	var n []string
	if o.Failed > 0 {
		n = append(n, fmt.Sprintf("LLM: %d of %d requests failed or were unreadable and were ignored", o.Failed, opts.Runs))
	}
	prose, ungrounded := 0, 0
	for _, r := range o.Results {
		if r.Tier == 6 {
			prose++
		}
		ungrounded += len(r.Ungrounded)
	}
	if prose > 0 {
		n = append(n, fmt.Sprintf("LLM: %d answers ignored the JSON format and were read from free text (less reliable)", prose))
	}
	if ungrounded > 0 {
		n = append(n, fmt.Sprintf("LLM: %d named locations are not in the files given to the model and were dropped", ungrounded))
	}
	return n
}

// Aggregate merges answers into findings, one per (file, function) pair.
// Votes = number of answers naming the pair (an answer counts once per
// pair). Ties are broken by the best (smallest) position the pair had in an
// answer, then by name, so the order is deterministic.
// Score is votes/runs; Evidence kind "llm" says "k/N answers".
func Aggregate(results []Result, runs int) []finding.Finding {
	if runs < len(results) {
		runs = len(results)
	}
	type agg struct {
		loc     finding.Location
		votes   int
		bestPos int
		lines   map[int]int
	}
	byKey := map[string]*agg{}
	var keys []string
	for _, r := range results {
		seen := map[string]bool{}
		for pos, l := range r.Locations {
			key := l.File + "|" + NormFunc(l.Func)
			if seen[key] {
				continue
			}
			seen[key] = true
			a := byKey[key]
			if a == nil {
				a = &agg{loc: finding.Location{File: l.File, Func: l.Func}, bestPos: pos, lines: map[int]int{}}
				byKey[key] = a
				keys = append(keys, key)
			}
			a.votes++
			if pos < a.bestPos {
				a.bestPos = pos
			}
			if l.Line > 0 {
				a.lines[l.Line]++
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := byKey[keys[i]], byKey[keys[j]]
		switch {
		case a.votes != b.votes:
			return a.votes > b.votes
		case a.bestPos != b.bestPos:
			return a.bestPos < b.bestPos
		}
		return keys[i] < keys[j]
	})
	var out []finding.Finding
	for i, k := range keys {
		a := byKey[k]
		loc := a.loc
		loc.Line = commonLine(a.lines)
		out = append(out, finding.Finding{
			Location: loc,
			Score:    float64(a.votes) / float64(runs),
			Rank:     i + 1,
			Evidence: []finding.Evidence{{
				Kind:   "llm",
				Detail: fmt.Sprintf("%d/%d answers name %s", a.votes, runs, describe(loc)),
			}},
		})
	}
	return out
}

func describe(l finding.Location) string {
	s := l.File
	if l.Func != "" {
		s += " " + l.Func
	}
	return strings.TrimSpace(s)
}

// commonLine returns the most voted line (smallest on ties), 0 if none.
func commonLine(lines map[int]int) int {
	best, bestN := 0, 0
	for l, n := range lines {
		if n > bestN || (n == bestN && l < best) {
			best, bestN = l, n
		}
	}
	return best
}

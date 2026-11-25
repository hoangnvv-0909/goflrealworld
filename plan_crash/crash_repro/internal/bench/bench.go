// Package bench measures "gofl crash" against the seeded bugs of a sample
// project (bugs.json): Acc@k at file, function and line level, the baseline
// "file of the first user frame", the pipeline rate and, optionally, the LLM
// layer with the paper's 3-of-5 rule (claim C12).
//
// Owner: Group C. The answers in bugs.json are read ONLY here, after ranking;
// nothing in the ranking code can see them.
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gofl/internal/agentprompt"
	"gofl/internal/crashflow"
	"gofl/internal/crashrank"
	"gofl/internal/finding"
	"gofl/internal/llm"
	"gofl/internal/logprofile"
	"gofl/internal/runner"
)

// Ks are the cut-offs reported.
var Ks = []int{1, 3, 5}

// Bug is one entry of bugs.json (the fields the benchmark needs).
type Bug struct {
	ID              string   `json:"id"`
	Cmd             string   `json:"cmd"`
	Kind            string   `json:"kind"`
	File            string   `json:"file"`
	Func            string   `json:"func"`
	Lines           []int    `json:"lines"`
	Trigger         string   `json:"trigger"`
	RelatedTriggers []string `json:"related_triggers"`
	Note            string   `json:"note"`
}

// Truth is the fix location.
func (b Bug) Truth() llm.Truth { return llm.Truth{File: b.File, Func: b.Func, Lines: b.Lines} }

// LoadBugs reads dir/bugs.json and keeps the entries of the given command.
func LoadBugs(dir, cmd string) ([]Bug, error) {
	data, err := os.ReadFile(filepath.Join(dir, "bugs.json"))
	if err != nil {
		return nil, err
	}
	var all []Bug
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("bugs.json: %w", err)
	}
	var out []Bug
	for _, b := range all {
		if b.Cmd == cmd {
			out = append(out, b)
		}
	}
	return out, nil
}

// Trigger is a parsed "KEY=VAL go test <pkgs> -run <re> ..." command line.
type Trigger struct {
	Env      []string
	Packages []string
	Run      string
}

var envRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// ParseTrigger understands the trigger strings of bugs.json: optional
// KEY=VALUE words, "go test", packages, -run (with single or double quotes)
// and ignored flags such as -count=1.
func ParseTrigger(s string) (Trigger, error) {
	words, err := splitWords(s)
	if err != nil {
		return Trigger{}, err
	}
	var t Trigger
	i := 0
	for i < len(words) && envRe.MatchString(words[i]) {
		t.Env = append(t.Env, words[i])
		i++
	}
	if i+1 >= len(words) || words[i] != "go" || words[i+1] != "test" {
		return Trigger{}, fmt.Errorf("trigger %q: want \"go test ...\"", s)
	}
	for i += 2; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "-run" && i+1 < len(words):
			i++
			t.Run = words[i]
		case strings.HasPrefix(w, "-run="):
			t.Run = strings.TrimPrefix(w, "-run=")
		case strings.HasPrefix(w, "-"):
			// -count=1 and friends: runner already uses -count=1
		default:
			t.Packages = append(t.Packages, w)
		}
	}
	if len(t.Packages) == 0 {
		t.Packages = []string{"./..."}
	}
	return t, nil
}

func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	var quote rune
	started := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, started = r, true
		case r == ' ' || r == '\t':
			if started || cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", s)
	}
	if started || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words, nil
}

// Source returns the crash text of one bug: the output of its trigger tests
// (all related triggers too, so grouping has something to group).
type Source func(ctx context.Context, b Bug) (text string, err error)

// RunTriggers is the live Source: it runs the triggers with the runner.
func RunTriggers(dir string, timeout time.Duration) Source {
	return func(ctx context.Context, b Bug) (string, error) {
		triggers := append([]string{b.Trigger}, b.RelatedTriggers...)
		var sb strings.Builder
		for _, trig := range triggers {
			t, err := ParseTrigger(trig)
			if err != nil {
				return "", err
			}
			res, err := runner.Run(ctx, runner.Spec{Dir: dir, Packages: t.Packages, Env: t.Env, Run: t.Run, Timeout: timeout})
			if err != nil {
				return "", err
			}
			if res.Unrunnable != nil {
				return sb.String(), fmt.Errorf("%s (%s): %s", b.ID, res.Unrunnable.Kind, res.Unrunnable.Detail)
			}
			sb.WriteString(res.Output())
			sb.WriteByte('\n')
		}
		return sb.String(), nil
	}
}

// Hits is Acc@k bookkeeping for one configuration.
type Hits struct {
	File, Func, Line map[int]*llm.Metric // by k
}

func newHits() Hits {
	h := Hits{File: map[int]*llm.Metric{}, Func: map[int]*llm.Metric{}, Line: map[int]*llm.Metric{}}
	for _, k := range Ks {
		h.File[k], h.Func[k], h.Line[k] = &llm.Metric{}, &llm.Metric{}, &llm.Metric{}
	}
	return h
}

func (h Hits) add(locs []finding.Location, t llm.Truth) {
	for _, k := range Ks {
		h.File[k].Add(llm.HitAt(locs, t, llm.LevelFile, k, 0))
		h.Func[k].Add(llm.HitAt(locs, t, llm.LevelFunc, k, 0))
		h.Line[k].Add(llm.HitAt(locs, t, llm.LevelLine, k, 0))
	}
}

// Case is the result for one bug.
type Case struct {
	ID       string
	Ran      bool   // the pipeline produced a ranked output
	Why      string // when it did not
	Events   int
	Groups   int
	Top      []finding.Location // first five locations, ranked
	Seconds  float64
	Baseline finding.Location // first user frame of the first event
	// By prompt configuration name ("ST", "ST+RoSFM", ...):
	LLM      map[string][]finding.Location // merged report after the vote
	LLMSolve map[string]bool               // "ST func@3" -> solved by the 3-of-5 rule
	LLMRaw   map[string][]string           // raw answers, kept so runs can be re-scored
}

// LLMResult is one prompt configuration over all cases.
type LLMResult struct {
	Config string
	Merged Hits // merged report after the LLM vote
	Quorum Hits // 3-of-5 rule on the individual answers (C12)
	Failed int  // answers that failed or could not be parsed
	Usage  llm.Usage
}

// Summary is the whole run.
type Summary struct {
	Cases        []Case
	Ranking      Hits // gofl crash without the LLM
	Baseline     Hits // first user frame only
	LLM          []LLMResult
	Ran, Total   int
	UsedLLM      bool
	LLMModel     string
	LLMRuns      int
	PriceIn      float64 `json:",omitempty"` // USD per million tokens
	PriceOut     float64 `json:",omitempty"`
	TotalSeconds float64
}

// Options for Run.
type Options struct {
	Dir      string
	Bugs     []Bug
	Source   Source
	LLMMode  string // "", "off", "fake", "local" or "api"
	Provider func(mode string) (llm.Provider, error)
	Show     io.Writer // where the LLM prompt is shown before sending; may be nil
	// Configs are the prompt configurations to measure (paper C's ablation);
	// nil means llm.DefaultConfig() only.
	Configs           []llm.PromptConfig
	PriceIn, PriceOut float64 // USD per million tokens, for the cost line
	// PromptDir, when set, receives the Agent prompts of every case that ran:
	// <ID>_E0.md (no ranking) and <ID>_E1.md (top 3 files). Nothing is sent.
	PromptDir string
}

// writeAgentPrompts writes the E0 and E1 prompts of bug b for its largest group.
func writeAgentPrompts(dir string, b Bug, g crashrank.Group, all []crashrank.Group) error {
	ranked, err := crashrank.Rank(g, all)
	if err != nil {
		return err
	}
	in := agentprompt.Input{Group: g, Ranked: ranked, Repro: append([]string{b.Trigger}, b.RelatedTriggers...)}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, m := range []agentprompt.Mode{agentprompt.E0, agentprompt.E1} {
		text, err := agentprompt.Build(in, m)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, b.ID+"_"+m.String()+".md"), []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// answerTagRe matches every comment that mentions "BUG:" (the tags that
// mark the seeded and collected faults, and file headers that explain them).
var answerTagRe = regexp.MustCompile(`[ \t]*(?://|/\*)[^\n]*\bBUG:[^\n]*`)

// StripAnswerTags removes the answer tags from source before it goes into a
// prompt; leaving them would hand the answer to the model (ported from
// TV3's llmeval). Lines are kept, so line numbers still match the stack.
func StripAnswerTags(src string) string {
	return answerTagRe.ReplaceAllString(src, "")
}

// Run measures every bug. Failures of one bug never stop the run: they count
// against the pipeline rate with a reason.
func Run(ctx context.Context, o Options) (Summary, error) {
	root, err := filepath.Abs(o.Dir)
	if err != nil {
		return Summary{}, err
	}
	prof, _ := logprofile.Get(logprofile.DefaultName)
	s := Summary{Ranking: newHits(), Baseline: newHits(), Total: len(o.Bugs), PriceIn: o.PriceIn, PriceOut: o.PriceOut}
	useLLM := o.LLMMode != "" && o.LLMMode != "off"
	s.UsedLLM, s.LLMRuns = useLLM, llm.DefaultRuns
	configs := o.Configs
	if len(configs) == 0 {
		configs = []llm.PromptConfig{llm.DefaultConfig()}
	}
	// One provider for the whole run, so its token counts add up.
	var prov llm.Provider
	var provErr error
	if useLLM {
		for _, c := range configs {
			s.LLM = append(s.LLM, LLMResult{Config: c.Name(), Merged: newHits(), Quorum: newHits()})
		}
		if prov, provErr = o.Provider(o.LLMMode); provErr == nil {
			s.LLMModel = llm.NameOf(prov)
		}
	}
	missLLM := func(t llm.Truth) {
		for i := range s.LLM {
			s.LLM[i].Merged.add(nil, t)
			s.LLM[i].Quorum.add(nil, t)
		}
	}
	opts := crashflow.Options{Root: root, ModulePath: crashflow.ModulePath(root), Source: "bench"}
	start := time.Now()

	for _, b := range o.Bugs {
		c := Case{ID: b.ID, LLM: map[string][]finding.Location{}, LLMSolve: map[string]bool{}, LLMRaw: map[string][]string{}}
		t0 := time.Now()
		text, err := o.Source(ctx, b)
		truth := b.Truth()
		if err != nil {
			c.Why = err.Error()
			s.Cases = append(s.Cases, c)
			// A case that did not run counts as a miss in every metric.
			s.Ranking.add(nil, truth)
			s.Baseline.add(nil, truth)
			missLLM(truth)
			continue
		}
		a, err := crashflow.Analyze(prof, strings.NewReader(text), opts)
		if err != nil {
			return s, err
		}
		c.Events, c.Groups = len(a.Events), len(a.Groups)
		if len(a.Events) == 0 {
			c.Why = "no panic or fatal error in the output of the trigger"
		} else {
			c.Ran = true
			s.Ran++
		}
		if o.PromptDir != "" && len(a.Groups) > 0 {
			if err := writeAgentPrompts(o.PromptDir, b, a.Groups[0], a.Groups); err != nil {
				return s, err
			}
		}
		locs := llm.FindingLocations(a.Report.Findings)
		c.Top = locs[:min(5, len(locs))]
		s.Ranking.add(locs, truth)

		// Baseline: the file of the first user frame of the first event.
		var base []finding.Location
		if len(a.Events) > 0 {
			if cp, ok := a.Events[0].Signature.CrashPoint(); ok {
				c.Baseline = finding.Location{File: cp.File, Line: cp.Line, Func: cp.Func}
				base = []finding.Location{c.Baseline}
			}
		}
		s.Baseline.add(base, truth)

		switch {
		case !useLLM:
		case provErr != nil:
			c.Why = "LLM: " + provErr.Error()
			missLLM(truth)
		case len(a.Groups) == 0:
			missLLM(truth)
		default:
			for i, cfg := range configs {
				r := &s.LLM[i]
				rep := a.Report
				before := llm.UsageOf(prov)
				_, out, err := crashflow.ApplyLLMWith(ctx, &rep, a.Groups[0], root, crashflow.LLMOptions{
					Mode: o.LLMMode, Config: cfg, Show: o.Show,
					Provider: func(string) (llm.Provider, error) { return prov, nil },
					Filter:   func(_, src string) string { return StripAnswerTags(src) },
				})
				r.Usage = addUsage(r.Usage, llm.UsageOf(prov).Sub(before))
				if err != nil {
					c.Why = strings.TrimPrefix(c.Why+"; ", "; ") + cfg.Name() + " LLM: " + err.Error()
				}
				if out != nil {
					r.Failed += out.Failed
					c.LLMRaw[cfg.Name()] = out.Raw
				}
				locs := llm.FindingLocations(rep.Findings)
				c.LLM[cfg.Name()] = locs
				r.Merged.add(locs, truth)
				for _, k := range Ks {
					for _, lv := range []llm.Level{llm.LevelFile, llm.LevelFunc, llm.LevelLine} {
						ok := out != nil && llm.Solved(out.Results, llm.DefaultRuns, truth, lv, k, 0)
						c.LLMSolve[fmt.Sprintf("%s %s@%d", cfg.Name(), lv, k)] = ok
						m := map[llm.Level]map[int]*llm.Metric{llm.LevelFile: r.Quorum.File, llm.LevelFunc: r.Quorum.Func, llm.LevelLine: r.Quorum.Line}[lv][k]
						m.Add(ok)
					}
				}
				if ctx.Err() != nil {
					return s, ctx.Err()
				}
			}
		}
		c.Seconds = time.Since(t0).Seconds()
		s.Cases = append(s.Cases, c)
	}
	s.TotalSeconds = time.Since(start).Seconds()
	return s, nil
}

// PipelineRate is the share of bugs for which a ranked output came out.
func (s Summary) PipelineRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return 100 * float64(s.Ran) / float64(s.Total)
}

// WriteMarkdown prints the result tables.
func (s Summary) WriteMarkdown(w io.Writer, layer string) {
	fmt.Fprintf(w, "# gofl crash benchmark (%s)\n\n", layer)
	fmt.Fprintf(w, "Cases: %d. Pipeline rate: %.0f%% (%d/%d ran to a ranked output). Time: %.1f s.\n\n",
		s.Total, s.PipelineRate(), s.Ran, s.Total, s.TotalSeconds)
	row := func(name string, h Hits) {
		fmt.Fprintf(w, "| %s |", name)
		for _, m := range []map[int]*llm.Metric{h.File, h.Func, h.Line} {
			for _, k := range Ks {
				fmt.Fprintf(w, " %.0f%% (%d/%d) |", m[k].Rate(), m[k].Hit, m[k].Total)
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprint(w, "| Configuration |")
	for _, lv := range []string{"file", "func", "line"} {
		for _, k := range Ks {
			fmt.Fprintf(w, " %s@%d |", lv, k)
		}
	}
	fmt.Fprint(w, "\n|---|")
	for range Ks {
		fmt.Fprint(w, "---:|---:|---:|")
	}
	fmt.Fprintln(w)
	row("gofl crash (no LLM)", s.Ranking)
	row("baseline: first user frame", s.Baseline)
	for _, r := range s.LLM {
		row(fmt.Sprintf("%s: gofl crash + LLM (merged report)", r.Config), r.Merged)
		row(fmt.Sprintf("%s: LLM alone, %d of %d answers rule (C12)", r.Config, llm.Quorum(s.LLMRuns), s.LLMRuns), r.Quorum)
	}
	fmt.Fprintf(w, "\nRanking vs baseline, file Acc@3: %+.0f points.\n", s.Ranking.File[3].Rate()-s.Baseline.File[3].Rate())
	if s.UsedLLM {
		fmt.Fprintf(w, "\nLLM: %s, %d independent answers per case and configuration; "+
			"\"// BUG:\" answer tags were removed from the source before prompting.\n\n", s.LLMModel, s.LLMRuns)
		fmt.Fprint(w, "| LLM config | merged vs no LLM, file@3 | merged vs no LLM, func@1 | failed answers | requests | input tokens | output tokens | truncated | cost (USD) |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, r := range s.LLM {
			cost := "-"
			if s.PriceIn > 0 || s.PriceOut > 0 {
				cost = fmt.Sprintf("%.2f", r.Usage.Cost(s.PriceIn, s.PriceOut))
			}
			fmt.Fprintf(w, "| %s | %+.0f | %+.0f | %d | %d | %d | %d | %d | %s |\n", r.Config,
				r.Merged.File[3].Rate()-s.Ranking.File[3].Rate(), r.Merged.Func[1].Rate()-s.Ranking.Func[1].Rate(),
				r.Failed, r.Usage.Requests, r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.Truncated, cost)
		}
	}
	fmt.Fprint(w, "\n| Case | Ran | Events | Groups | Top-1 | Note |\n|---|---|---:|---:|---|---|\n")
	for _, c := range s.Cases {
		top := ""
		if len(c.Top) > 0 {
			top = fmt.Sprintf("`%s:%d`", c.Top[0].File, c.Top[0].Line)
		}
		fmt.Fprintf(w, "| %s | %v | %d | %d | %s | %s |\n", c.ID, c.Ran, c.Events, c.Groups, top, c.Why)
	}
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{Requests: a.Requests + b.Requests, InputTokens: a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens, Truncated: a.Truncated + b.Truncated}
}

// Command goflagent runs the repair Agent (Claude Code, headless) on the crash
// cases of a sample project and scores it (plan step 11).
//
//	goflagent -dir testdata/goflsample -case C9 -mode e1 -runs 1 -out results/agent
//	goflagent -dir testdata/goflrealworld -mode both -runs 3 -out results/agent_realworld
//	goflagent -summary results/agent_realworld
//
// Every run lands in <out>/<ID>_<E0|E1>_<n>.json, with the patch in .diff.
// A case counts as solved by the 2-of-3 rule (-runs 3). The Agent works in a
// copy of the project outside the repository, without bugs.json and without
// the "// BUG:" tags. It costs money: use -max-budget and start with -case.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gofl/internal/agentprompt"
	"gofl/internal/agentrun"
	"gofl/internal/bench"
	"gofl/internal/crashflow"
	"gofl/internal/crashrank"
	"gofl/internal/logprofile"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("goflagent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "testdata/goflsample", "project with bugs.json")
	only := fs.String("case", "", "comma list of case IDs (default: all crash cases)")
	mode := fs.String("mode", "both", "e0 (no ranking), e1 (top 3 files) or both")
	runs := fs.Int("runs", 3, "runs per case and mode")
	out := fs.String("out", "results/agent", "directory for the results")
	model := fs.String("model", "claude-sonnet-5-5", "Claude model, pinned so every run uses the same one (\"\" = default of the installation)")
	budget := fs.Float64("max-budget", 2, "USD cap per Agent run (0 = none)")
	timeout := fs.Duration("timeout", 20*time.Minute, "time limit per Agent run")
	summary := fs.String("summary", "", "only summarise the results in this `dir`")
	resume := fs.Bool("resume", false, "skip runs whose result file already exists in -out")
	dryPrompts := fs.Bool("dry", false, "print what would run and the prompts' sizes; do not start the Agent")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *summary != "" {
		if err := writeSummary(stdout, *summary); err != nil {
			fmt.Fprintln(stderr, "goflagent:", err)
			return 1
		}
		return 0
	}
	var modes []agentprompt.Mode
	switch *mode {
	case "e0":
		modes = []agentprompt.Mode{agentprompt.E0}
	case "e1":
		modes = []agentprompt.Mode{agentprompt.E1}
	case "both":
		modes = []agentprompt.Mode{agentprompt.E0, agentprompt.E1}
	default:
		fmt.Fprintf(stderr, "goflagent: unknown -mode %q\n", *mode)
		return 2
	}
	if !*dryPrompts {
		if err := agentrun.Check(""); err != nil {
			fmt.Fprintln(stderr, "goflagent:", err)
			return 1
		}
	}
	bugs, err := bench.LoadBugs(*dir, "crash")
	if err != nil {
		fmt.Fprintln(stderr, "goflagent:", err)
		return 1
	}
	if *only != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*only, ",") {
			want[strings.TrimSpace(id)] = true
		}
		var keep []bench.Bug
		for _, b := range bugs {
			if want[b.ID] {
				keep = append(keep, b)
			}
		}
		bugs = keep
	}
	if len(bugs) == 0 {
		fmt.Fprintln(stderr, "goflagent: no case selected")
		return 1
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "goflagent:", err)
		return 1
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(stderr, "goflagent:", err)
		return 1
	}
	ctx := context.Background()
	prof, _ := logprofile.Get(logprofile.DefaultName)
	src := bench.RunTriggers(root, 3*time.Minute)

	for _, b := range bugs {
		text, err := src(ctx, b)
		if err != nil {
			fmt.Fprintf(stderr, "goflagent: %s: cannot reproduce: %v\n", b.ID, err)
			continue
		}
		a, err := crashflow.Analyze(prof, strings.NewReader(text), crashflow.Options{Root: root, ModulePath: crashflow.ModulePath(root), Source: "agent"})
		if err != nil || len(a.Groups) == 0 {
			fmt.Fprintf(stderr, "goflagent: %s: no crash group\n", b.ID)
			continue
		}
		ranked, err := crashrank.Rank(a.Groups[0], a.Groups)
		if err != nil {
			fmt.Fprintf(stderr, "goflagent: %s: %v\n", b.ID, err)
			continue
		}
		in := agentprompt.Input{Group: a.Groups[0], Ranked: ranked, Repro: append([]string{b.Trigger}, b.RelatedTriggers...)}
		var baseline []string
		if !*dryPrompts {
			// Tests that pass now (in a clean copy) must still pass after the fix.
			tmp, err := os.MkdirTemp("", "goflagent-base-*")
			if err != nil {
				fmt.Fprintln(stderr, "goflagent:", err)
				return 1
			}
			if err = bench.AgentWorkspace(root, tmp); err == nil {
				baseline, err = agentrun.BaselineTests(ctx, tmp, 3*time.Minute)
			}
			os.RemoveAll(tmp)
			if err != nil {
				fmt.Fprintf(stderr, "goflagent: %s: baseline: %v\n", b.ID, err)
				continue
			}
			fmt.Fprintf(stderr, "%s: %d baseline tests pass\n", b.ID, len(baseline))
		}
		for _, m := range modes {
			prompt, err := agentprompt.Build(in, m)
			if err != nil {
				fmt.Fprintf(stderr, "goflagent: %s: %v\n", b.ID, err)
				continue
			}
			for n := 1; n <= *runs; n++ {
				if *resume {
					if _, err := os.Stat(filepath.Join(*out, fmt.Sprintf("%s_%s_%d.json", b.ID, m, n))); err == nil {
						continue
					}
				}
				if *dryPrompts {
					fmt.Fprintf(stdout, "%s %s run %d: prompt %d bytes\n", b.ID, m, n, len(prompt))
					continue
				}
				fmt.Fprintf(stderr, "%s %s run %d/%d ...\n", b.ID, m, n, *runs)
				r, err := agentrun.Run(ctx, agentrun.Spec{
					Project: root, Bug: b, Mode: m, Prompt: prompt, Baseline: baseline, Run: n,
					Model: *model, MaxBudget: *budget, Timeout: *timeout,
				})
				if err != nil {
					fmt.Fprintf(stderr, "goflagent: %s %s run %d: %v\n", b.ID, m, n, err)
					continue
				}
				if err := save(*out, r); err != nil {
					fmt.Fprintln(stderr, "goflagent:", err)
					return 1
				}
				fmt.Fprintf(stdout, "%s %s run %d: fixed=%v func@1=%v func@3=%v repro=%v others=%v root=%v $%.2f %.0fs %d turns %s\n",
					b.ID, m, n, r.Fixed, r.LocFunc1, r.LocFunc3, r.Reproduced, r.OthersPass, r.RootCause, r.CostUSD, r.Seconds, r.Turns, r.AgentError)
			}
		}
	}
	return 0
}

func save(dir string, r agentrun.Result) error {
	base := filepath.Join(dir, fmt.Sprintf("%s_%s_%d", r.Case, r.Mode, r.Run))
	diff := r.Diff
	r.Diff = ""
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(base+".json", data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(base+".diff", []byte(diff), 0o644)
}

// writeSummary prints, per mode, how many cases are solved by the 2-of-3 rule
// (more than half of the runs of the case) and the mean cost.
func writeSummary(w io.Writer, dir string) error {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		return fmt.Errorf("no results in %s", dir)
	}
	type key struct{ c, m string }
	runs := map[key][]agentrun.Result{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var r agentrun.Result
		if json.Unmarshal(data, &r) != nil || r.Case == "" {
			continue
		}
		runs[key{r.Case, r.Mode}] = append(runs[key{r.Case, r.Mode}], r)
	}
	var keys []key
	for k := range runs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].m != keys[j].m {
			return keys[i].m < keys[j].m
		}
		return keys[i].c < keys[j].c
	})
	fmt.Fprintln(w, "| Case | Mode | Runs | Fixed | func@1 | func@3 | file@3 | Cost USD | Turns | Seconds | In tokens | Out tokens |")
	fmt.Fprintln(w, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	type tot struct {
		cases, fixed, f1, f3, file3, runs, turns, in, out int
		cost, secs                                        float64
	}
	totals := map[string]*tot{}
	for _, k := range keys {
		rs := runs[k]
		var fixed, f1, f3, file3, turns, in, out int
		var cost, secs float64
		for _, r := range rs {
			fixed += b2i(r.Fixed)
			f1 += b2i(r.LocFunc1)
			f3 += b2i(r.LocFunc3)
			file3 += b2i(r.LocFile3)
			turns += r.Turns
			in += r.InputTokens
			out += r.OutputTokens
			cost += r.CostUSD
			secs += r.Seconds
		}
		n := len(rs)
		fmt.Fprintf(w, "| %s | %s | %d | %d/%d | %d/%d | %d/%d | %d/%d | %.3f | %.1f | %.0f | %d | %d |\n", k.c, k.m, n,
			fixed, n, f1, n, f3, n, file3, n, cost/float64(n), float64(turns)/float64(n), secs/float64(n), in/n, out/n)
		t := totals[k.m]
		if t == nil {
			t = &tot{}
			totals[k.m] = t
		}
		t.runs += n
		t.turns += turns
		t.in += in
		t.out += out
		t.cost += cost
		t.secs += secs
		t.cases++
		more := func(n int) bool { return 2*n > len(rs) }
		t.fixed += b2i(more(fixed))
		t.f1 += b2i(more(f1))
		t.f3 += b2i(more(f3))
		t.file3 += b2i(more(file3))
	}
	fmt.Fprintln(w, "\nCases solved by majority of runs (2 of 3):")
	for _, m := range []string{"E0", "E1"} {
		if t := totals[m]; t != nil {
			fmt.Fprintf(w, "- %s: fixed %d/%d, func@1 %d/%d, func@3 %d/%d, file@3 %d/%d\n", m, t.fixed, t.cases, t.f1, t.cases, t.f3, t.cases, t.file3, t.cases)
			n := float64(t.runs)
			fmt.Fprintf(w, "  per run (%d runs): $%.3f, %.1f turns, %.0f s, %d input tokens, %d output tokens\n",
				t.runs, t.cost/n, float64(t.turns)/n, t.secs/n, t.in/t.runs, t.out/t.runs)
		}
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

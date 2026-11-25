// Command goflbench measures "gofl crash" on the seeded bugs of a sample
// project: Acc@1/3/5 at file, function and line level, against the baseline
// "file of the first user frame", with and without the LLM layer.
//
//	goflbench -dir testdata/goflsample
//	goflbench -dir testdata/goflrealworld -layer realworld -json out.json
//	GOFL_LLM_MODEL=llama3 goflbench -dir testdata/goflsample -llm local -configs all
//	GOFL_LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=... goflbench -dir testdata/goflrealworld -llm api -price-in 4 -price-out 20
//	goflbench -dir testdata/goflsample -llm fake -configs all   # whole LLM path, offline
//
// The answers (bugs.json) are read only to score. Layer 1 (goflsample) is for
// development; go/no-go uses layer 2 (docs/THRESHOLDS.md).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gofl/internal/bench"
	"gofl/internal/llm"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("goflbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "testdata/goflsample", "project with bugs.json")
	layer := fs.String("layer", "", "label for the report (default: directory name)")
	mode := fs.String("llm", "off", "LLM layer: off, fake (offline, first frame), local or api (needs GOFL_LLM_* in the environment)")
	configs := fs.String("configs", "default", "with -llm: prompt configurations to measure: all, default, or a comma list of st, st+rank, sm, sm+rank (paper C ablation)")
	priceIn := fs.Float64("price-in", 0, "with -llm: USD per million input tokens, for the cost column")
	priceOut := fs.Float64("price-out", 0, "with -llm: USD per million output tokens")
	timeout := fs.Duration("timeout", 3*time.Minute, "time limit per trigger run")
	jsonOut := fs.String("json", "", "also write the summary as JSON to `file`")
	promptDir := fs.String("agent-prompt-dir", "", "write the repair-Agent prompts (<ID>_E0.md, <ID>_E1.md) of every case to `dir`; nothing is sent")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	switch *mode {
	case "off", "fake", "local", "api":
	default:
		fmt.Fprintf(stderr, "goflbench: unknown -llm mode %q\n", *mode)
		return 2
	}
	cfgs, err := llm.ParseConfigs(*configs)
	if err != nil {
		fmt.Fprintln(stderr, "goflbench:", err)
		return 2
	}
	bugs, err := bench.LoadBugs(*dir, "crash")
	if err != nil {
		fmt.Fprintln(stderr, "goflbench:", err)
		return 1
	}
	if len(bugs) == 0 {
		fmt.Fprintln(stderr, "goflbench: no crash bugs in", *dir)
		return 1
	}
	label := *layer
	if label == "" {
		label = *dir
	}
	sum, err := bench.Run(context.Background(), bench.Options{
		Dir: *dir, Bugs: bugs, Source: bench.RunTriggers(*dir, *timeout),
		LLMMode: *mode, Provider: llm.NewProvider, Show: stderr,
		Configs: cfgs, PriceIn: *priceIn, PriceOut: *priceOut, PromptDir: *promptDir,
	})
	if err != nil {
		fmt.Fprintln(stderr, "goflbench:", err)
		return 1
	}
	sum.WriteMarkdown(stdout, label)
	if *jsonOut != "" {
		data, err := json.MarshalIndent(sum, "", "  ")
		if err == nil {
			err = os.WriteFile(*jsonOut, data, 0o644)
		}
		if err != nil {
			fmt.Fprintln(stderr, "goflbench:", err)
			return 1
		}
	}
	return 0
}

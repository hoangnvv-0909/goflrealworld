package main

// Group C owns this file: the body of "gofl crash". The pipeline itself
// lives in internal/crashflow so the benchmark runner uses the same code.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gofl/internal/agentprompt"
	"gofl/internal/crashflow"
	"gofl/internal/crashrank"
	"gofl/internal/finding"
	"gofl/internal/llm"
	"gofl/internal/logprofile"
	"gofl/internal/report"
	"gofl/internal/runner"
)

// newProvider builds the LLM backend; tests replace it.
var newProvider = llm.NewProvider

// agentPromptDoc is the JSON printed by "gofl crash -agent-prompt -format json".
// Files is what E1 shows the Agent; it is also filled in E0, where the prompt
// does not mention it, so an editor can still mark the files gofl suspects.
type agentPromptDoc struct {
	Version int                    `json:"version"`
	Mode    string                 `json:"mode"`
	Prompt  string                 `json:"prompt"`
	Events  int                    `json:"events"`
	Groups  int                    `json:"groups"`
	Files   []agentprompt.FileHint `json:"files"`
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, "; ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// reproCommand is the "go test" line that -test/-run ran.
func reproCommand(pkgs, run string) string {
	if strings.TrimSpace(pkgs) == "" {
		pkgs = "./..."
	}
	cmd := "go test " + strings.Join(strings.Fields(pkgs), " ")
	if run != "" {
		cmd += " -run '" + run + "'"
	}
	return cmd
}

func runCrash(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gofl crash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c common
	addCommon(fs, &c)
	logFile := fs.String("log", "", "read crashes from log `file` instead of stdin")
	profile := fs.String("profile", "default", "log profile used with -log: a name, or the path of a JSON profile `file`")
	llmMode := fs.String("llm", "off", "LLM triage: off, local or api (off by default)")
	dir := fs.String("dir", ".", "project root: paths in the output are relative to it")
	testPkgs := fs.String("test", "", "run \"go test\" on these `packages` (e.g. ./...) and read the crashes from it")
	run := fs.String("run", "", "with -test: only tests matching this `regexp` (implies -test ./...)")
	dry := fs.Bool("llm-dry-run", false, "with -llm: print what would be sent to the model and send nothing")
	agentPrompt := fs.Bool("agent-prompt", false, "print the repair-Agent prompt for the largest crash group instead of the report; nothing is sent")
	agentMode := fs.String("agent-mode", "e1", "with -agent-prompt: e1 (crash facts plus the top 3 files) or e0 (crash facts only)")
	var repro stringList
	fs.Var(&repro, "repro", "with -agent-prompt: a command that reproduces the crash (repeatable); a log has none, so say it here")
	if err := parse(fs, args, &c); err != nil {
		return err
	}
	switch *llmMode {
	case "off", "local", "api":
	default:
		fmt.Fprintf(stderr, "gofl crash: unknown -llm mode %q (want off, local or api)\n", *llmMode)
		return errUsage
	}
	runTests := *testPkgs != "" || *run != ""
	if runTests && *logFile != "" {
		fmt.Fprintln(stderr, "gofl crash: -test/-run and -log cannot be used together")
		return errUsage
	}
	prof, err := logprofile.Lookup(*profile)
	if err != nil {
		fmt.Fprintln(stderr, "gofl crash:", err)
		return errUsage
	}
	format, _ := report.ParseFormat(c.format) // validated by parse

	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}

	var (
		notes  []string
		text   io.Reader
		source = "stdin"
		runErr error
	)
	switch {
	case runTests:
		pkgs := strings.Fields(*testPkgs)
		if len(pkgs) == 0 {
			pkgs = []string{"./..."}
		}
		res, err := runner.Run(context.Background(), runner.Spec{
			Dir: root, Packages: pkgs, Env: c.env, Timeout: c.timeout, Run: *run,
		})
		if err != nil {
			return err
		}
		source = "go test"
		text = strings.NewReader(res.Output())
		if res.Unrunnable != nil {
			notes = append(notes, fmt.Sprintf("tests could not run (%s): %s", res.Unrunnable.Kind, res.Unrunnable.Detail))
			runErr = fmt.Errorf("tests could not run (%s)", res.Unrunnable.Kind)
		}
	case *logFile != "":
		f, err := os.Open(*logFile)
		if err != nil {
			return err
		}
		defer f.Close()
		source = filepath.Base(*logFile)
		if text, err = crashflow.SniffTestJSON(f); err != nil {
			return err
		}
	default:
		if text, err = crashflow.SniffTestJSON(stdin); err != nil {
			return err
		}
	}

	a, err := crashflow.Analyze(prof, text, crashflow.Options{
		Root: root, ModulePath: crashflow.ModulePath(root), Source: source,
	})
	if err != nil {
		return err
	}
	rep := a.Report
	rep.Notes = append(notes, rep.Notes...)
	if len(a.Events) == 0 && runErr == nil {
		rep.Notes = append(rep.Notes, "no panic or fatal error found in the input")
	}

	if *agentPrompt {
		if len(a.Groups) == 0 {
			fmt.Fprintln(stderr, "gofl crash: no crash found, so there is no Agent prompt")
			return errUsage
		}
		mode := agentprompt.E1
		switch strings.ToLower(*agentMode) {
		case "e1":
		case "e0":
			mode = agentprompt.E0
		default:
			fmt.Fprintf(stderr, "gofl crash: unknown -agent-mode %q (want e0 or e1)\n", *agentMode)
			return errUsage
		}
		in := agentprompt.Input{Group: a.Groups[0], Repro: repro}
		if in.Ranked, err = crashrank.Rank(a.Groups[0], a.Groups); err != nil {
			return err
		}
		if runTests && len(in.Repro) == 0 {
			in.Repro = []string{reproCommand(*testPkgs, *run)}
		}
		text, err := agentprompt.Build(in, mode)
		if err != nil {
			return err
		}
		if format == report.JSON {
			// Structured form for editors: the prompt and exactly the files
			// the Agent was told about (E1), so a visual note can match it.
			doc := agentPromptDoc{
				Version: finding.SchemaVersion, Mode: mode.String(), Prompt: text,
				Events: len(a.Events), Groups: len(a.Groups),
				Files: agentprompt.Hints(in.Ranked, agentprompt.TopFiles, agentprompt.FuncsPerFile),
			}
			data, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return err
			}
			text = string(data) + "\n"
		}
		_, err = io.WriteString(stdout, text)
		if err == nil {
			err = runErr
		}
		return err
	}

	if *llmMode != "off" {
		if len(a.Groups) == 0 {
			rep.Notes = append(rep.Notes, "LLM triage skipped: no crash to analyse")
		} else {
			n, _, err := crashflow.ApplyLLM(context.Background(), &rep, a.Groups[0], root, *llmMode, *dry, newProvider, stderr)
			rep.Notes = append(rep.Notes, n...)
			if err != nil {
				// The deterministic ranking is still valid: keep it and say
				// why the LLM part is missing.
				rep.Notes = append(rep.Notes, "LLM triage was not applied: "+err.Error())
			}
		}
	}

	var w io.Writer = stdout
	if c.out != "" {
		f, err := os.Create(c.out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if err := report.Write(w, format, rep); err != nil {
		return err
	}
	return runErr
}

package main

// Group C owns this file: the body of "gofl crash".

import (
	"flag"
	"fmt"
	"io"
)

func runCrash(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gofl crash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c common
	addCommon(fs, &c)
	logFile := fs.String("log", "", "read crashes from log `file` instead of stdin")
	profile := fs.String("profile", "default", "log profile used with -log")
	llmMode := fs.String("llm", "off", "LLM triage: off, local or api (off by default)")
	if err := parse(fs, args, &c); err != nil {
		return err
	}
	_, _, _ = logFile, profile, llmMode
	// TODO(TV1): wire crashsig.Parse -> crashrank.GroupEvents -> crashrank.Rank
	// -> (optional) llm -> report.Write.
	return fmt.Errorf("crash: %w", errNotImplemented)
}

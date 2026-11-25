package main

// Group E owns this file: the body of "gofl perf".

import (
	"flag"
	"fmt"
	"io"
)

func runPerf(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gofl perf", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c common
	addCommon(fs, &c)
	bench := fs.String("bench", "", "benchmark `regexp` with size sub-benchmarks (n=...)")
	base := fs.String("base", "", "existing pprof `file` at base size (use with -scaled)")
	scaled := fs.String("scaled", "", "existing pprof `file` at scaled size (use with -base)")
	topK := fs.Int("top", 5, "number of hotspots to report")
	if err := parse(fs, args, &c); err != nil {
		return err
	}
	if (*base == "") != (*scaled == "") {
		fmt.Fprintln(stderr, "gofl perf: -base and -scaled must be given together")
		return errUsage
	}
	if *bench == "" && *base == "" {
		fmt.Fprintln(stderr, "gofl perf: need -bench, or -base and -scaled")
		return errUsage
	}
	_ = topK
	// TODO(TV4): runner.Run (bench at two sizes, -cpuprofile) -> diffprof.Compute
	// -> diffprof.Paths -> report.Write.
	return fmt.Errorf("perf: %w", errNotImplemented)
}

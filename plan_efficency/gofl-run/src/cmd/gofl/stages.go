package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gofl/internal/benchrun"
	"gofl/internal/contract"
	"gofl/internal/diffprof"
	"gofl/internal/pprofread"
	"gofl/internal/report"
)

// stageMaker builds the later stages of gofl perf from the options of the
// run, which are known only after the flags are parsed.
type stageMaker func(sp stageParams) stages

// stageParams is what the later stages need from the command line.
type stageParams struct {
	Opts   benchrun.Options
	Hash   bool   // -hash-names
	Entry  string // -entry
	RankBy string // -rank-by as given: "", raw or perop
	Stderr io.Writer
}

// assemble is what gofl perf runs after Compute: Duc Anh's Paths, then
// Hieu's report.
func assemble(sp stageParams) stages {
	o, entry := sp.Opts, sp.Entry
	if entry == "" && o.Bench != "" { // benchmark mode: the benchmark is the entry point
		entry = strings.TrimSpace(o.Bench)
		if !strings.HasPrefix(entry, "Benchmark") {
			entry = "Benchmark" + entry
		}
	}
	method := diffprof.MethodAuto // not asked for: paper, and stack when paper cannot
	switch o.PathsMethod {
	case contract.PathsPaper:
		method = diffprof.MethodPaper
	case contract.PathsStack:
		method = diffprof.MethodStack
	}
	return stages{
		Paths:  pathsStage(o.Dir, entry, method, sp.RankBy, sp.Stderr),
		Report: reportStage(sp.Hash),
	}
}

// pathsStage reads run.json, hotspots.json and the two profiles of out,
// writes critical_paths.json and records in run.json the method it used.
// When the hotspots were ranked per operation (the default in benchmark
// mode), the scores are per operation too: each profile's seconds are
// multiplied by diffprof.Scale before they are subtracted.
func pathsStage(dir, entry, method, rankBy string, stderr io.Writer) func(context.Context, string) error {
	return func(ctx context.Context, out string) error {
		runPath := filepath.Join(out, contract.RunFile)
		run, err := contract.ReadRun(runPath)
		if err != nil {
			return err
		}
		var hs []diffprof.Hotspot
		data, err := os.ReadFile(filepath.Join(out, contract.HotspotsFile))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &hs); err != nil {
			return fmt.Errorf("%s: %v", contract.HotspotsFile, err)
		}
		if run.BasePprof == nil || run.ScaledPprof == nil {
			return &diffprof.Error{Code: contract.NoProfile, Msg: "run.json names no profile"}
		}
		var p [2]*pprofread.Profile
		for i, name := range []string{*run.BasePprof, *run.ScaledPprof} {
			p[i], err = pprofread.Load(contract.Resolve(runPath, name), pprofread.Options{Root: dir})
			if err != nil {
				return &diffprof.Error{Code: contract.NoProfile, Msg: err.Error()}
			}
		}
		popt := diffprof.PathsOptions{Method: method, Entry: entry}
		if diffprof.ResolveRank(rankBy, run.Bench) == diffprof.RankPerOp && run.Bench != nil {
			fb, _ := diffprof.Scale(p[0], run.Bench.BaseNsPerOp)
			fs, _ := diffprof.Scale(p[1], run.Bench.ScaledNsPerOp)
			if fb > 0 && fs > 0 {
				popt.BaseFactor, popt.ScaledFactor = fb, fs
			} else {
				fmt.Fprintln(stderr, "gofl: warning: paths: no usable scale factor, scores are in raw seconds")
			}
		}
		res, err := diffprof.Paths(p[0], p[1], hs, popt)
		if err != nil {
			return err // a *diffprof.Error carries ENTRY_NOT_COVERING
		}
		if res.Stats.FallbackReason != "" {
			fmt.Fprintf(stderr, "gofl: warning: paths: method paper failed (%s); used stack\n", res.Stats.FallbackReason)
		}
		paths := res.Paths
		if paths == nil {
			paths = []diffprof.CriticalPath{} // the contract wants [] and never null
		}
		if err := contract.WriteJSON(filepath.Join(out, contract.PathsFile), paths); err != nil {
			return err
		}
		run.PathsMethod = res.Method
		return contract.WriteJSON(runPath, run)
	}
}

// reportDir is the folder of out that holds report.md and report.json.
const reportDir = "report"

// reportStage writes out/report/report.md and report.json. The report
// package refuses an existing folder, so the one of an earlier run, which
// this tool wrote, is removed first.
func reportStage(hashNames bool) func(context.Context, string) error {
	return func(ctx context.Context, out string) error {
		target := filepath.Join(out, reportDir)
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		return report.Generate(out, target, hashNames)
	}
}

# gofl

Fault localization for Go projects, based on two papers:

| Command | Paper | Group |
|---|---|---|
| `gofl crash` | Integrating Crash Report Mining and LLMs for Bug Localization and Repair (C) | C |
| `gofl perf` | EffiHolmes: Differential Profiling-Guided ... Fix Localization (E) | E |

SemLoc (`gofl sem`) is an optional branch, not in v0.1.
Start with `START_HERE.md`. Plan: `../ke_hoach_3_nhom_fault_localization.tex`.

## Status

`gofl crash` (paper C) is implemented end to end: `crashsig`, `crashrank`,
`logprofile`, `llm`, `runner`, SARIF output, the pipeline library
`internal/crashflow` and the benchmark runner `cmd/goflbench`. `gofl perf`
(`diffprof`) is still a stub that returns `ErrNotImplemented`.

```sh
# crash from a pipe, a log file, or by running the tests
go test ./crash -run TestNth 2>&1 | gofl crash -dir testdata/goflsample
gofl crash -dir testdata/goflrealworld -log testdata/goflrealworld/stacks/server_crash.log
gofl crash -dir testdata/goflsample -test ./crash -run '^TestNth$' -format sarif

# optional LLM triage (off by default; the exact prompt is printed to stderr first)
GOFL_LLM_MODEL=llama3 gofl crash -llm local -llm-dry-run   # show, send nothing
GOFL_LLM_MODEL=llama3 gofl crash -llm local
GOFL_LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=... gofl crash -llm api -llm-dry-run

# measure Acc@k against bugs.json, with the baseline "first user frame"
go run ./cmd/goflbench -dir testdata/goflsample
go run ./cmd/goflbench -dir testdata/goflrealworld
# LLM layer: the four prompt configurations of paper C; "-llm fake" runs the whole path offline.
# "// BUG:" answer tags are removed from the source before prompting.
go run ./cmd/goflbench -dir testdata/goflsample -llm fake -configs all
GOFL_LLM_PROVIDER=anthropic go run ./cmd/goflbench -dir testdata/goflrealworld -llm api -configs all -price-in 4 -price-out 20 -json out.json
```

## Layout

| Path | Owner | What |
|---|---|---|
| `cmd/gofl` | TV1 (`crash.go`: C, `perf.go`: E) | CLI (`gofl crash`, `gofl perf`) |
| `internal/finding` | TV1 | Shared result type (contract). `testdata/example.json` is the JSON the extension develops against |
| `internal/report` | TV1 | JSON, Markdown, SARIF output |
| `internal/crashsig` | C | Parse and normalise Go panic stacks |
| `internal/crashrank` | C | Group crashes (4 levels), rank files (IAD x IBF x FF) |
| `internal/llm` | C | Optional LLM triage, off by default |
| `internal/crashflow` | C | The whole `gofl crash` pipeline as a library (used by the CLI and the benchmark) |
| `internal/bench` | C | Acc@k against `bugs.json`, baseline, LLM 3-of-5 rule; `cmd/goflbench` is its command line |
| `internal/logprofile` | C | Plugin point for log formats |
| `internal/runner` | E | Execution harness: `go test -json`, benchmarks, `-cpuprofile`, timeout |
| `internal/diffprof` | E | Differential profiling: hotspots and compressed paths |
| `../../extension` | C | VS Code extension (see its README) |
| `internal/benchcheck` | TV1 | Test that keeps `bugs.json` consistent with the `BUG:` tags in the sample |
| `testdata/goflsample` | TV1 | Sample project with 20 seeded bugs (C1-C13, P1-P3, S1-S4) and answers in `bugs.json` |
| `testdata/goflrealworld` | TV1 | Forked RealWorld backend (handler, service, repository, SQLite) with 9 seeded crash bugs (W1-W9), 8 perf bugs (WP1-WP8), a bulk data seeder and real server crash logs; see its `README.md` and `NOTICE.md` |
| `docs/` | TV1 | `CONTRACT.md` (interfaces, flags, decisions), `THRESHOLDS.md` (go/no-go), `DECISIONS.md` (open items for the group) |

## Build and test

```sh
go build ./cmd/gofl
go vet ./...
go test ./...
```

`go test ./...` skips `testdata/`, so the intentionally failing tests of
`goflsample` and `goflrealworld` do not break CI. To run them, `cd` into the
sample directory first.

## Rules

- Contract packages (`finding`, `report`, CLI flags, exported types of every
  package) change only through TV1: see `docs/CONTRACT.md` section 6.
- Work only inside the directories you own (table above). Two people editing
  the same file in separate copies cannot be merged safely.
- The module path `gofl` in `go.mod` is a placeholder. It works as is; change it
  only if the project is later published, and then in one step for all imports.
- `.github/` (CI, CODEOWNERS) only matters if the project is later put on a git
  host. Ignore it for now; run the checks locally (see START_HERE.md).

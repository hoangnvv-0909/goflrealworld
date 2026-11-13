# gofl

Fault localization for Go projects, based on two papers:

| Command | Paper | Group |
|---|---|---|
| `gofl crash` | Integrating Crash Report Mining and LLMs for Bug Localization and Repair (C) | C |
| `gofl perf` | EffiHolmes: Differential Profiling-Guided ... Fix Localization (E) | E |

SemLoc (`gofl sem`) is an optional branch, not in v0.1.
Start with `START_HERE.md`. Plan: `../ke_hoach_3_nhom_fault_localization.tex`.

## Status

Skeleton only. Every package has its contract types and stub functions that
return `ErrNotImplemented`. Implemented so far: CLI flag parsing,
`report` JSON and Markdown.

## Layout

| Path | Owner | What |
|---|---|---|
| `cmd/gofl` | TV1 (`crash.go`: C, `perf.go`: E) | CLI (`gofl crash`, `gofl perf`) |
| `internal/finding` | TV1 | Shared result type (contract). `testdata/example.json` is the JSON the extension develops against |
| `internal/report` | TV1 | JSON, Markdown, SARIF output |
| `internal/crashsig` | C | Parse and normalise Go panic stacks |
| `internal/crashrank` | C | Group crashes (4 levels), rank files (IAD x IBF x FF) |
| `internal/llm` | C | Optional LLM triage, off by default |
| `internal/logprofile` | C | Plugin point for log formats |
| `internal/runner` | E | Execution harness: `go test -json`, benchmarks, `-cpuprofile`, timeout |
| `internal/diffprof` | E | Differential profiling: hotspots and compressed paths |
| `editors/vscode` | C | VS Code extension (not started) |
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

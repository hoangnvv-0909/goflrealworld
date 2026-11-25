# goflsample

Sample Go project with **seeded bugs** for developing/measuring `gofl` (benchmark layer 1).
Ground truth is in `bugs.json` (file, function, lines) and in `// BUG:<id>` comments.

| Package | Bugs | Command | Owner |
|---|---|---|---|
| `crash` | C1-C7 panics / fatal errors (`crash.go`); C8-C13 hard cases (`hard.go`) | `gofl crash` | group C |
| `perf`  | P1-P3 N+1, quadratic loop, repeated serialize | `gofl perf` (benchmarks `n=100/1000/5000`) | group E |
| `sem`   | S1-S4 boundary, timezone, operator, sign error | `gofl sem` | optional branch |

C8-C13 are the "hard" crash cases: the panic site is often not the fix site.
`bugs.json` gives the fix location (`file`/`func`/`lines`) and, where it
differs, the `crash_point`. Each has a `hard` field saying what it tests.

| Id | What makes it hard |
|---|---|
| C8 | 6-frame stack across five functions |
| C9 | one root cause, three different stacks (`related_triggers`): merge only at level 4 (same crash-point file) |
| C10 | crash in `Cache.evict`, fault in `NewCache` |
| C11 | top frame is the generic helper `must[...]`; fault is in the caller |
| C12 | deep recursion: Go prints `...N frames elided...` |
| C13 | panic in a goroutine: no `testing.tRunner`, stack ends with `created by` |

`go test ./...` is **expected to fail**. A panic aborts its whole package binary, so run crash
tests one at a time (see `trigger` in `bugs.json`). C7 needs `GOFL_RUN_FATAL=1`.

Caveat (paper section 7): seeded bugs are easier than real ones; do not use these numbers for go/no-go.

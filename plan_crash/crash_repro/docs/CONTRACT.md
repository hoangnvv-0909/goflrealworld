# Interface contract (v1)

Owner: TV1. Status: **proposed by TV1 in week 1, day 1-3; frozen at gate G1**
once both groups have approved it. After that, any change needs a change request
to TV1 (see section 6) and an announcement to both groups.

The Go source is the authority for exact signatures. This document fixes what
the source cannot say: meaning, units, invariants, flags and decisions.

## 1. The `finding` types (`internal/finding`)

`finding.Report` is the only thing a command produces. Every output format is
a rendering of it.

| Field | Meaning | Invariant |
|---|---|---|
| `Version` | `finding.SchemaVersion` (now `1`) | Bumped on any incompatible JSON change. The extension must refuse a newer major version. |
| `Command` | `"crash"` or `"perf"` | |
| `Findings` | suspicious locations, most suspicious first | `Rank` is 1-based and consecutive; sorted by `Rank` |
| `Notes` | limits the user must know | Never silently drop a limit: unrunnable tests, missing profile data, I/O wait not visible |

`Location`:

- `File` is **slash-separated and relative to the project root**, on every OS.
  Never absolute, never backslashes. Benchmark answers and the extension both
  depend on this.
- `Line`, `Col` are 1-based; `0` means unknown (omitted in JSON).
- `Func` is `pkg.Func` or `pkg.(*Type).Method` as Go prints it in stacks,
  with generic instantiation suffixes and `.funcN` closure suffixes stripped
  by `crashsig.Normalize`.

`Finding.Score` is comparable only inside one report. `Evidence.Kind` is a
machine tag. `finding.go` gives examples (`stack-rank`, `crash-group`,
`delta-self`, `ndc`, `llm`); a producer may add a tag, but must list it in
that comment in the same change, and tags are never renamed.
`Finding.Flow` is an optional execution path (entry first); it is what maps to
SARIF `codeFlows`.

JSON rules: field names are fixed by the struct tags; unknown fields must be
ignored by readers (so adding a field does not need a version bump, removing
or retyping one does).

## 2. Command line

Draft flags from the plan, now fixed. Both commands share the common flags.

| Flag | Commands | Default | Meaning |
|---|---|---|---|
| `-format md\|json\|sarif` | both | `md` | output format |
| `-o file` | both | stdout | write output to a file |
| `-timeout d` | both | `5m` | time limit for running tests or benchmarks |
| `-env KEY=VALUE` | both | none | extra environment for the test process; repeatable |
| `-log file` | crash | stdin | read crashes from a log instead of stdin |
| `-profile name\|file.json` | crash | `default` | `logprofile` used with `-log`: a registered name, or a JSON file `{"name": "...", "strip_prefix": "regexp"}` for a logger with an unusual line prefix |
| `-dir path` | crash | `.` | project root; output paths are relative to it, `go.mod` gives the module path |
| `-test pkgs`, `-run regexp` | crash | none | run `go test -json` through `runner` and read the crashes from it; exit 1 with the reason in `Notes` if the tests cannot run |
| `-agent-prompt` | crash | off | print the repair-Agent prompt of the largest crash group instead of the report; with `-format json` print `{version, mode, prompt, events, groups, files:[{file, score, funcs:[{func, line, score}]}]}` (what the VS Code extension reads). Sends nothing |
| `-agent-mode e0\|e1` | crash | `e1` | with `-agent-prompt`: `e1` = crash facts plus the top 3 files, `e0` = crash facts only. `files` is filled in both |
| `-repro cmd` | crash | none | with `-agent-prompt`: a command that reproduces the crash (repeatable). `-test`/`-run` fill it in by themselves |
| `-llm-dry-run` | crash | off | with `-llm`: print the prompt, send nothing |
| `-llm off\|local\|api` | crash | `off` | LLM triage. **Off by default**; the key is supplied by the user |
| `-bench regexp` | perf | none | benchmark with size sub-benchmarks (`n=...`) |
| `-base file`, `-scaled file` | perf | none | use existing pprof files; must be given together |
| `-top k` | perf | `5` | number of hotspots |

LLM environment (keys are read from the environment only, never from flags or files):
`GOFL_LLM_PROVIDER` (`openai`, the default: any OpenAI-compatible server; or `anthropic`),
`GOFL_LLM_URL`, `GOFL_LLM_MODEL`, `GOFL_LLM_KEY` (fallbacks `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`),
`GOFL_LLM_TIMEOUT` (default `5m`), `GOFL_LLM_TEMPERATURE` (OpenAI-compatible only, default 1.0),
`GOFL_LLM_EFFORT` and `GOFL_LLM_FALLBACK=default` (Anthropic only; the refusal fallback is off by
default because it would mix two models in one measurement). With `anthropic`, URL and model default
to `https://api.anthropic.com/v1` and `claude-opus-5-5`.

Exit codes: `0` success (also `-h`), `1` runtime error (including "tests could
not run", which also puts a concrete reason into `Notes`), `2` usage error.
Results go to stdout (or `-o`), diagnostics to stderr, never mixed.

Not in v0.1: `gofl sem`. The `sem` package and flags are a later, optional
branch and are not part of this contract.

## 3. Package contracts

Each is the exported API in the file named below. TV1 reviews every change to
an exported name, type or doc comment here.

| Package | File | Contract points that matter to others |
|---|---|---|
| `crashsig` | `crashsig.go` | `Parse` takes **one** panic stack and drops panic machinery frames (`runtime.gopanic`, `sigpanic`, `testing.tRunner`, ...) so crash point is the first user frame. `Hash` is stable across runs and OSes. `Subsumes` see section 5. Parser must accept the truncated form `...N frames elided...` and goroutine stacks ending in `created by`. |
| `crashrank` | `crashrank.go` | `GroupEvents` returns groups with `Level` 1..4 (1 identical, 2 equivalent signature, 3 containment, 4 same crash-point file). `Rank` returns `[]finding.Finding`, already ranked. The IAD, IBF, FF formulas are **not in paper C**; TV2 writes the chosen formulas into the package doc. |
| `runner` | `runner.go` | Never edits sources in place, always honours `Timeout`, cleans temporary directories. If tests cannot run it returns `Result.Unrunnable` with a `Reason`, not an error and not silence. |
| `diffprof` | `diffprof.go` | `Compute` takes a `Pair` of pprof files at two sizes and returns top-k `Hotspot`s. `Paths` returns compressed `(file, func)` paths, no line numbers. |
| `llm` | `llm.go` | `Provider` is the only thing that talks to a model. `Redact` runs on everything before it leaves the machine. `ParseResponse` tolerates text around the JSON and truncated output. |
| `logprofile` | `logprofile.go` | Streaming only: `Entries` must not read the whole log into memory. |

Dependencies point one way: `cmd/gofl` -> `report`, `crashrank`, `diffprof`,
`runner`, `llm`, `logprofile` -> `crashsig` and `finding`. `finding` imports
nothing from the project.

## 4. SARIF mapping (provisional)

Pending TV7's verification of SARIF 2.1.0 and how VS Code shows it. Until then
SARIF is **not promised** to the extension; the extension reads the JSON.

Planned mapping, to be confirmed by TV7: one `result` per `Finding`;
`rank` = `Score` scaled to 0-100 (SARIF `rank` is a number from 0 to 100);
`locations[0]` = `Location`; `codeFlows[0].threadFlows[0].locations` = `Flow`;
`message.text` = joined `Evidence`; `Notes` go to
`invocations[0].toolExecutionNotifications`.

## 5. Decisions made by TV1

### C3: is level-3 containment contiguous? **Yes, contiguous.**

Paper C (section II-A, Level 3) merges two groups when one stack is
"structurally contained" in the other, comparing fully qualified method names
and ignoring line numbers. It does not say whether gaps are allowed.

We require the shorter stack to appear in the longer one as a contiguous run,
same order. Reasons:

1. Level 4 (same crash-point file) already provides the broad merge, so level 3
   should be the precise one. False merges at level 3 also inflate IBF, which
   penalises files shared across groups.
2. Go stacks are short and full of wrappers; a gap-tolerant check would merge
   unrelated crashes that only share `main` and the test runner.

Consequence worth knowing: the three stacks of sample bug C9 (same fault, three
callers) differ in their second frame, so they do **not** merge at level 3;
they merge at level 4 because they share the crash-point file.

Revisit after the layer-2 measurement. `crashsig.Subsumes` is the only place
that changes.

### Open items that need the whole group, not TV1 alone

See `docs/DECISIONS.md`.

## 6. Change process

The project is shared as copies of one folder, not through a shared
repository, so nobody edits the contract in their own copy and keeps going.

1. Send TV1 a message (group chat) with: what you need changed, why, and which
   packages or people it affects. Do not change `finding`, `report`, the flags
   or an exported name of another person's package in your own copy and carry on.
2. TV1 decides, edits **the Go source and this file together**, and posts the
   change to the group, saying whether both groups are affected.
3. Everyone then copies the changed files into their own copy.
4. Bump `finding.SchemaVersion` only for an incompatible JSON change, and say
   so in the announcement.

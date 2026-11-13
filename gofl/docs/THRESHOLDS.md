# Go / no-go thresholds

Owner: TV1 writes it; **the whole group signs it** at gate G1 (plan, week 1,
day 4-5). The numbers below are TV1's proposal: edit them in a PR if the group
agrees on others, but they must not change after G1 without saying why.
Nothing here is measured yet.

## Metrics

- **Acc@k** at file, function and line level: the share of cases where the
  ground-truth location is among the first k findings. Ground truth comes from
  `bugs.json` (layer 1) or from the collected fix commit (layer 2).
- **Pipeline rate**: share of collected candidate cases for which the whole
  command ran to a ranked output (TV5 records the failure reason of the rest).
- **Run time** per case, in seconds.

## Layers

| Layer | Source | Use |
|---|---|---|
| 1 | seeded bugs in `testdata/goflsample` | development only. Easier than real bugs; **never** used for go/no-go |
| 2 | 10-20 real cases per command, collected in weeks 3-4 | go/no-go |

Layer 2 has 10-20 cases, so **one case is 5-10 percentage points**. A result
is preliminary evidence, and the report and release notes must say so.

## Thresholds (layer 2)

| Command | Criterion | Threshold | Meaning if met |
|---|---|---|---|
| `gofl crash` | file Acc@3 | **>= 60%** | feature is supported in v0.1 |
| `gofl crash` | file Acc@3 minus the baseline "file of the first user frame" | **>= +10 points** | the ranking is worth having (answers C15) |
| `gofl perf` | function Acc@5 | **>= 50%** | feature is supported in v0.1 |
| both | pipeline rate | **>= 70%** | otherwise v0.1 states which projects work |

(X = 60% and Y = 50% in the wording of the plan.)

## Decision rule at gate G4

- Criterion met: ship the command as supported.
- Criterion missed: still ship it, labelled **experimental**, with the measured
  number in the README and release notes. Do not hide or round up.
- If `gofl crash` misses the baseline margin, say so in the report: for Go
  stacks, reading the first frame is as good as the ranking. That is a result,
  not a failure of the project.

## LLM

`gofl crash -llm` is measured with and without the LLM on the same cases; a case
counts as correct when at least 3 of 5 independent answers are (claim C12).
There is **no go/no-go gate** for the LLM: it stays opt-in and off by default
either way. The report states the Acc@3 difference and, if an API was used, the
cost. Quality of the explanation is not graded (no two-person manual review in
4 weeks).

## Not measured

LLM-generated fixes (out of scope, claim C16), `gofl sem`.

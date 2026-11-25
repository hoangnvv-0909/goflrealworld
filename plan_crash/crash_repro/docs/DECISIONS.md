# Decisions for the whole group

Week 1, day 1-3. TV1 cannot settle these alone: each needs the group (and some
need an account or a budget only a person can provide). Status is
**OPEN** until someone writes the answer and the date in the last column.

| # | Question | TV1's proposal | Why | Decided |
|---|---|---|---|---|
| 1 | Project name | keep `gofl` | already in code, CLI and plan | OPEN |
| 2 | Licence | Apache-2.0 | permissive, has an explicit patent grant; MIT is the simpler alternative. No `LICENSE` file is added until this is decided | OPEN |
| 3 | Where the code lives | zip, no git host | decided by TV1 for the group: the project is handed over as a zip. Add a shared repository later if that changes | closed |
| 4 | Go module path | keep the placeholder `gofl` | works offline; only matters if the project is published | closed |
| 5 | LLM budget | default is **no LLM**. Develop and measure against one local model first (needs no money and no data leaves the machine); use a paid API only if the group sets a spend cap in advance | `gofl crash` must work fully without an LLM; privacy (`Redact`) | OPEN |
| 6 | Account names for `CODEOWNERS` | not needed | no git host, the file is ignored | closed |
| 7 | Confirm TVn assignment | roles were assigned provisionally by list order (plan section 2) | nobody has said who is strong at what | OPEN |
| 8 | What the repair Agent gets from the ranking | the **top 3 files** of `crashrank.Rank` (3 functions each), passed straight to the Agent with no LLM in between | same 3 files as paper C's prompt; matches the file@3 metric; the E1-minus-E0 gap then measures exactly the value of the top 3 | closed 2026-11-24 |
| 9 | Order of functions inside a file (plan step 8) | keep the current weight `share/(1+dist)` for now | paper C orders by frequency only, so this **deviates from the paper**; B1 numbers must say so. Revisit if B1 should be faithful | closed 2026-11-24 (deferred) |
| 10 | LLM hint to the Agent (experiment E2) | dropped | the LLM layer stays only in B1, to reproduce paper C | closed 2026-11-24 |
| 11 | Which copy of the code is the main one | `plan_crash/crash_repro`; `gofl/` is deleted last, after a commit and with confirmation | `gofl/` has no file that `crash_repro` lacks | closed 2026-11-24 |

## What is already done and needs no decision

- CI matrix (Linux, Windows, macOS; Go 1.23.x and stable): `.github/workflows/ci.yml`.
- Contract and flags: `docs/CONTRACT.md`. Thresholds: `docs/THRESHOLDS.md`.
- Sample project with 20 seeded bugs: `testdata/goflsample`, answers in `bugs.json`,
  checked by `internal/benchcheck`.

## Notes that affect the decisions above

- Item 5: a local model is also what makes the 5-answers-per-case measurement
  (claim C11) affordable. Five calls per case over the layer-1 and layer-2 cases
  is the main cost driver if an API is chosen.

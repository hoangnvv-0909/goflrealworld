# START HERE

You received `gofl` as a zip. There is no shared repository. The work is split
**by paper**: group C (crash) and group E (EffiHolmes) each own a set of
directories (section 3). Work in your own copy, stay inside your group's
directories, and hand the files back as described in section 5.

## 1. Set up (10 minutes)

You need Go 1.23 or newer (`go version`).

```sh
cd gofl
go vet ./...
go test ./...
go build -o gofl ./cmd/gofl      # on Windows: -o gofl.exe
./gofl crash -h
```

All of it must pass before you start. `gofl crash` and `gofl perf` still print
"not implemented yet": that is the work.

`go test ./...` never runs `testdata/`. The sample project has failing tests on
purpose; see `testdata/goflsample/README.md` and run them one at a time with the
`trigger` command of each bug in `testdata/goflsample/bugs.json`.

## 2. Read, in this order

1. `../ke_hoach_3_nhom_fault_localization.tex` (the plan, one level above this folder): your row in section 2 and
   your week-by-week items in section 4. Note the **gates** G1-G4.
2. `docs/CONTRACT.md`: the types and flags everyone shares.
3. `docs/THRESHOLDS.md`: how we decide go / no-go at the end.
4. The paper(s) in `../papers/` named in the plan's "Đọc trước" column for you.
5. `docs/DECISIONS.md`: items that are still open.

## 3. Where you work: by paper

Two groups, one per paper. Each group owns a set of directories; **inside the
group you split the work among yourselves** (the plan suggests who does what,
see section 4 of the plan).

| Group | Paper | Members | Edit only |
|---|---|---|---|
| **C** | crash report mining (`gofl crash`) | TV1, TV2, TV3, TV7 | `internal/crashsig`, `internal/crashrank`, `internal/llm`, `internal/logprofile`, `editors/vscode`, `cmd/gofl/crash.go`, `testdata/goflsample/crash/`, plus the shared area below |
| **E** | EffiHolmes (`gofl perf`) | TV4, TV5, TV6 | `internal/runner`, `internal/diffprof`, `cmd/gofl/perf.go`, `testdata/goflsample/perf/` |

**Shared area, owned by TV1 (group C), changed only on request:**
`internal/finding`, `internal/report`, `cmd/gofl/main.go`, `internal/benchcheck`,
`testdata/goflsample/bugs.json`, `docs/`. Group E asks TV1 for changes here.

Inside a group, two people must not edit the same file in separate copies:

- Group C: TV2 `crashsig` and `crashrank`, TV3 `llm` and `logprofile`, TV7
  `editors/vscode`, TV1 the shared area and `crash.go`.
- Group E: TV5 `runner`; TV4 and TV6 both work in `diffprof`, so use separate
  files (for example `compute.go` for TV4, `calltree.go` and `paths.go` for
  TV6), and TV4 owns `perf.go`.

The `TVn` names in code comments are only that suggested split.
Put new code in **new files** of your package when you can.
When you add seeded bugs to `testdata/goflsample`, add them in your own package
(`crash/` or `perf/`) and send the `bugs.json` lines to TV1.

## 4. Rules

- Do not change anything outside your group's directories. If you need a change
  in the other group's package or in the shared area (`finding`, `report`,
  `cmd/gofl/main.go`, flags, an exported name), ask TV1
  (`docs/CONTRACT.md` section 6).
- Write tests next to your code (`*_test.go`). Use stacks and profiles taken from
  the real `goflsample`, not invented ones.
- Keep `gofmt -l .` and `go vet ./...` clean before you send anything.
- Never put real keys, tokens or private data into the project.

## 5. Handing work back

Hand-over days are fixed by the plan's gates: **end of weeks 1, 2, 3 and 4**.

Each group has one **integrator**, who collects the members' directories into a
single copy and sends one zip. Suggested: TV1 for group C, TV4 for group E
(the group may pick someone else; tell TV1).

1. Each member runs `gofmt -l .`, `go vet ./...` and `go test ./...` in their
   copy, then sends the integrator the directories or files they changed.
2. The integrator copies them into the group's copy, runs the three checks again,
   and zips **only the group's directories** (section 3), keeping their paths.
3. Name it `<C or E>_week<k>.zip` and send it to TV1 (group C's integrator is TV1,
   so group C only needs step 4).
4. TV1 merges both groups into the master copy, runs the checks, and sends
   everyone the new full zip. **Replace your copy with it** before starting the
   next week.

A change outside your group's directories will not be merged: that is why the
rule above exists.

## 6. Sample project answers

`testdata/goflsample/bugs.json` holds the correct locations for 20 seeded bugs.
Do not let your code read them while ranking (that would be cheating). Only the
benchmark runner reads them, to compute Acc@k.

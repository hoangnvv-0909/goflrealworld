# goflrealworld

Benchmark layer 2 for `gofl`: a real multi-layer Go backend (RealWorld / Conduit API) with
**seeded bugs**. It gives crash stacks and server logs much closer to production than the
unit-level `goflsample`. Source and license: see `NOTICE.md`.

Layers a request goes through:

```
gorilla/mux -> CORS -> router closure -> JWT middleware -> handler -> service -> repository -> SQLite (modernc, pure Go)
```

Ground truth is in `bugs.json` (same schema as `goflsample`) and in `// BUG:<id>` comments.
`internal/benchcheck` in `gofl` checks that the two agree.

## Crash bugs (`gofl crash`)

| Id | Panic | Fault (fix here) | Crash point | What makes it hard |
|---|---|---|---|---|
| W1 | nil pointer | `service/user.go` `GetUserByID` | `handler/user.go` `GetCurrentUser` | fault function already returned: not on the stack |
| W2 | slice bounds `[:40] with length 6` | `utils/slug.go` `GenerateSlug` (guard checks title, not slug) | same | 2 entry points (create, update) |
| W3 | `interface {} is float64, not int` | `handler/auth.go` `RefreshToken` | same | classic `encoding/json` pitfall |
| W4 | assignment to entry in nil map | `service/tag.go` `invalidatePopular` | `service/tag.go` `GetPopularTags` | order dependent (write, then read); fault never on the stack |
| W5 | nil pointer | `service/comment.go` `DeleteComment` (`:=` shadowing) | same function, 8 lines later | faulty line looks correct alone |
| W6 | index out of range in a goroutine | `service/comment.go` `mentions` | same | stack ends with `created by ...CreateComment`; **kills the process** |
| W7 | `panic(err)` from `utils.Must` | `handler/comment.go` `DeleteComment` | `utils/must.go` `Must[...]` | top frame is a generic helper |
| W8 | nil pointer | `repository/article.go` `GetBySlug` (returns `nil, nil`) | 4 functions in `service/article.go` | one cause, 4 stacks, merge only at level 4; fault not on any stack |
| W9 | integer divide by zero | `handler/article.go` `GetArticlesFeed` | same | default applied to a by-value copy in the service |

## Perf bugs (`gofl perf`)

| Id | Origin | Function | DB? | Grows with | Measured (n small → large) |
|---|---|---|---|---|---|
| WP1 | seeded | `utils.NormalizeTags` (dedup with `slices.Contains`) | no | tags in a request, quadratic | 23 µs → 34 ms (n=100→5000) |
| WP2 | **upstream** | `utils.GenerateSlug` (regexp compiled per call) | no | linear, `regexp.compile` dominates | ~108 µs per slug |
| WP3 | **upstream** | `ArticleService.GetArticles` (N+1: 3 queries per article) | yes | page size (capped at 100) | 4.4 → 40 ms (n=10→100) |
| WP4 | seeded | `UserRepository.GetByUsername` (`LOWER(username)` defeats the index) | yes | users in the table | 136 → 710 µs; fixed: flat ~125 µs |
| WP5 | seeded | `TagRepository.GetPopularTags` (one `COUNT` query per tag) | yes | tags in the whole DB | 13 → 670 ms |
| WP6 | seeded | `ArticleService.GetArticlesFeed` (reads every article, filters in Go) | yes | articles in the whole DB (page stays 10) | 5.5 → 127 ms; fixed: 4.0 → 4.9 ms |
| WP7 | seeded | `utils.StripTags` (`out += string(r)`) | no | body bytes, quadratic | 108 µs → 163 ms (1 KB→50 KB); fixed: 5 → 237 µs |
| WP8 | seeded | `utils.HashPassword` (bcrypt cost 14, upstream 10) | no | nothing: constant 16× | 1.05 s vs 63 ms per hash |

Benchmarks (`trigger` in `bugs.json`):

```sh
go test ./internal/utils      -run '^$' -bench 'NormalizeTags|GenerateSlug|StripTags|HashPassword'
go test ./internal/repository -run '^$' -bench 'GetByUsername|GetPopularTags'
go test ./internal/service    -run '^$' -bench 'GetArticles'      # WP3 and WP6
go test ./internal/service    -run '^$' -bench '^BenchmarkGetArticlesFeed$' -cpuprofile cpu.pprof
```

What each one tests in `gofl perf`:

- **Hotspot in the driver, fix in our code** (WP3, WP4, WP5, WP6). The SQLite driver is pure Go,
  so database time **is** in the CPU profile, as `modernc.org/sqlite/lib` frames. With a
  networked DB (Postgres) it would be I/O wait and would not show up in a CPU profile.
  WP4's fix is the SQL text, not Go code. In WP6 the user frame nearest the hotspot,
  `repository.GetArticles`, is correct upstream code: the fault is its caller.
- **Hotspot in the runtime** (WP7): `concatstrings`, `memmove`, `mallocgc`, GC.
- **Hotspot in a library, fix is one constant** (WP8). It does not grow with n, so comparing a
  base load with a scaled load shows nothing. Compare with `BenchmarkHashPasswordDefaultCost`.
- Tests `TestPopularTagsMatchesUpstream` and `TestFeedMatchesUpstream` check that WP5 and WP6
  return exactly what the upstream query returns: the bugs are slow, not wrong.

Side effect of WP8: register and login take ~1 s each, and every test binary that imports
`testutil` spends ~1 s at start hashing one password.

## Data

Tests and benchmarks build their own database in a temp dir: `testutil.Seeded` (3 users, 2
articles, used by the crash triggers) and `Env.Fill`, which bulk-loads n rows with package
`internal/seed` (used by the DB benchmarks).

A server started with `go run ./cmd/server` begins with an **empty** `realworld.db`. To see the
perf bugs through the API, fill it first with `cmd/seed` (server stopped):

```sh
go run ./cmd/seed -fresh      # 5 000 users, 20 000 articles, 2 000 tags, 100 000 follows,
                              # 100 000 favorites, 60 000 comments: ~120 MB, ~15 s
go run ./cmd/seed -users 500 -articles 2000 -tags 300    # smaller; see -h for all flags
```

Every seeded user (`user00000` … ) has password `password123`. Measured on the default data set:
login ≈ 1.3 s (WP8), `GET /api/articles/feed?limit=10` ≈ 2 s (WP6),
`GET /api/articles?limit=20` ≈ 0.25 s (WP3), `GET /api/profiles/user04242` ≈ 3–10 ms (WP4).

The server also serves pprof on `localhost:6060` (`PPROF_ADDR=off` disables it):

```sh
go tool pprof -top -cum "http://localhost:6060/debug/pprof/profile?seconds=10"
```

A 10 s profile while calling the feed puts 88 % of CPU in `modernc.org/sqlite`, under
`repository.GetArticles` ← `service.GetArticlesFeed`.

## Running crash triggers

All commands run from this directory. `go test ./...` is **expected to fail**: every crash
trigger panics and aborts its package binary, so run triggers one at a time (`trigger` in
`bugs.json`):

```sh
go test ./internal/app -run '^TestHappyPaths$' -count=1   # must PASS: bugs are independent
go test ./internal/app -run '^TestW8_GetMissingArticle$' -count=1
```

`TestHappyPaths` calls every endpoint that holds a bug, with normal input. It passing shows
that each bug fires only on its own trigger.

### Server logs with many crashes

```sh
go run ./cmd/server 2> crash.log      # listens on :8080, DB file realworld.db
go run ./cmd/loadgen -n 500           # another terminal; add -fatal to end with W6
```

`net/http` recovers handler panics and logs `http: panic serving <addr>: <msg>` followed by
the goroutine stack, so the server keeps running. W6 panics outside a handler and kills it,
which is why loadgen only sends it with `-fatal`, as the last request.

## Fixtures in `stacks/`

- `W*_*.txt`: full output of each trigger test (Go 1.25), one file per trigger.
- `server_crash.log`: stderr of the server under `loadgen -n 500 -fatal`: 142 handler panics
  (W1×18, W2×17, W3×18, W4×21, W5×16, W7×20, W8×15, W9×17), then the fatal W6 crash.

Local paths are replaced by `<root>`, `<modcache>` and `<goroot>`. Note for `crashsig`:
Go 1.25 writes `[recovered, repanicked]` after the panic message in test output.

Caveat (same as `goflsample`): seeded bugs are easier than real ones, and the `BUG:` comments
must be stripped before code is shown to an LLM, or they give the answer away.

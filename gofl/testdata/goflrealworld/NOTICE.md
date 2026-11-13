# NOTICE: source and license of this code

## Source

The code under `cmd/server`, `internal/{config,db,handler,middleware,model,repository,service,utils}`
and `migrations/` is a fork of the `backend/` directory of:

- Repository: https://github.com/Hands-On-Vibe-Coding/realworld-vibe-coding
- Commit: `3c8fe1fc16a48a56a87f309a9ef2e86bb3873562` (2025-07-05)
- Forked on: 2026-11-13

`README.upstream.md` is the upstream `backend/README.md`, kept unchanged for reference
(its Docker/AWS instructions do not apply here).

## License status (checked 2026-11-13)

- The upstream `README.md` says: *"This project is licensed under the MIT License - see the
  [LICENSE](LICENSE) file for details."*
- The repository has **no `LICENSE` file**, and the GitHub API reports `license: null`.
  So no copyright holder or license text was published with the code.

We treat the README line as the authors' intent to use MIT, credit the source here, and use the
fork **only for internal coursework** (benchmark data for `gofl`). Do not redistribute it outside
the course without checking the license again.

## Changes made in this fork

| Change | Files |
|---|---|
| Module path `github.com/hands-on-vibe-coding/realworld-vibe-coding/backend` → `goflrealworld` | all `.go`, `go.mod` |
| SQLite driver `mattn/go-sqlite3` (cgo) → `modernc.org/sqlite` (pure Go); unused `lib/pq` dropped | `internal/db/drivers_sqlite.go`, `internal/db/database.go`, `go.mod` |
| Routing moved from `main.go` to `app.NewRouter` (unchanged routes) | `internal/app/router.go`, `cmd/server/main.go` |
| `Database.MigrateDir` added | `internal/db/database.go` |
| `gofmt -w` on all files | all `.go` |
| Removed: `realworld.db`, prebuilt binaries `server` and `realworld-backend`, Dockerfiles, Makefile | — |
| Migration progress messages go to `MigrationManager.Log` (stdout by default, discarded by `MigrateDir`) so they do not mix with benchmark output | `internal/db/migrations.go`, `internal/db/database.go` |
| pprof served on `PPROF_ADDR` (default `localhost:6060`) | `cmd/server/main.go` |
| Added: seeded bugs (`BUG:W*`, `BUG:WP1`, `BUG:WP4`–`BUG:WP8`), tags on upstream perf issues (`BUG:WP2`, `BUG:WP3`) | see `bugs.json` |
| Added: `internal/testutil`, `internal/seed`, `internal/app/*_test.go`, `*/perf_test.go`, `cmd/loadgen`, `cmd/seed`, `UserRepository.FollowingIDs`, `stacks/`, `bugs.json`, this file, `README.md` | — |

Seeded bugs add small features or rewrites that did not exist upstream (popular-tags cache,
`ttl_hours` on token refresh, `@mention` notifications, slug length limit, `X-Total-Pages` on
the feed, `utils.Must`, case-insensitive profile lookup, HTML tags stripped from article bodies,
popular tags and the feed computed in Go, bcrypt cost 14). Upstream `GetFeedArticles` is kept,
unused, as the reference for WP6. All other behavior is upstream code.

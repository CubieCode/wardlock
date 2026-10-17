---
name: wardlock-go-service
description: Write or review Go in the Wardlock server, CLI or API client, covering package layout, errors, logging, storage behind repository interfaces, portable SQL, tests and owner isolation.
---

# Go service code

Read docs/ARCHITECTURE.md and docs/MODULES.md first. Repository decisions override generic Go advice. No new dependency without a reason in the PR; the wave's lockfile owner adds it.

**Layout.** Server domain packages live in `apps/server/internal/<area>` (`edge`, `access`, `deploys`, `sites`, `owners`, `tls`, `store`, `api`, `platform`). Lower-case single-word package names; no `util`, `common` or `helpers`. Only `cmd/wardlockd` wires packages together. The CLI imports only `wardlock.nz/apiclient`; MIT code never imports AGPL code. Respect the depguard rules in `.golangci.yml`; never silence them with `//nolint`.

**Shape.** `context.Context` is the first parameter of anything that does I/O. Dependencies arrive in an explicit struct or constructor arguments: no package-level mutable state, no `init()` side effects, no service locators. Inject clocks (`func() time.Time`) and randomness where tests need control. Accept interfaces, return concrete types; declare an interface in the package that consumes it, small and specific.

**Errors.** Wrap with `fmt.Errorf("doing x: %w", err)`. Domain packages export sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrForbidden`); check them with `errors.Is`. Only `internal/api` (and the edge for site responses) maps them to HTTP status. Never put secrets, tokens or request bodies in error strings.

**Logging.** `log/slog` with typed attrs (`slog.String`, `slog.Int`), logger passed in, never the global default in library code. Wrap secret values in `platform.Secret`; log URLs only through `platform.RedactURL`. Never log raw tokens, cookies, passwords, codes or Authorization headers.

**Storage.** `store` exposes per-area repository interfaces (`Sites`, `Deploys`, `Links`, `Sessions`, `AccessLog`, ...). Domain packages take those interfaces, never `*sql.DB`, `*sql.Tx` or `sqlcgen` types. Implement them in `store` with sqlc queries in `internal/store/queries/*.sql` and run `pnpm codegen`. Transactions are explicit: a repository method that needs one takes or opens it visibly; no hidden ambient transaction. Blobs go through `BlobStore` (`Has`, `Put`, `Open`, `Delete`), rate limits through `Limiter`, cache invalidation through `Notifier`.

**Portable SQL.** Every query and migration must also run on Postgres:
- `INSERT ... ON CONFLICT (...) DO UPDATE`, never `INSERT OR REPLACE`/`REPLACE`; no SQLite-only functions (`datetime()`, `strftime()`, `ifnull()`, `glob`), no `AUTOINCREMENT`.
- Named parameters `sqlc.arg(name)`/`sqlc.narg(name)`, never `?` or `$1`.
- `RETURNING` only with column lists Postgres accepts in the same form.
- Text IDs (UUIDv7 or ULID) generated in Go. Timestamps are UTC Unix milliseconds in `INTEGER` columns (Postgres `BIGINT`); booleans are `INTEGER NOT NULL CHECK (x IN (0, 1))`. Note the Postgres type in a comment wherever it differs.
- Migrations are `NNNN_snake_case.sql`, forward only, never edited after merge; only the wave's migration owner adds one.
- Every new repository method gets a case in that interface's contract test suite (run against `storetest.New`), so a future Postgres implementation must pass the same tests.

**Owner scoping.** Every owner-scoped query takes the owner ID from the verified session or API token, never from a request body, path or query. Include it in the `WHERE` clause of reads, updates, deletes, lists and nested lookups. A missing resource and another owner's resource both return the same 404 with the same body. Once a second owner can exist, add two-owner isolation tests: owner B cannot read, list, update, delete or reference owner A's resources by guessed ID.

**Tests.** Table-driven unit tests; `httptest` for handlers (status, headers, body), with real routing rather than calling handler methods directly. `t.Context()`, `t.TempDir()`, no sleeps where a channel or poll with deadline works. `go test -race` must pass. Prefer real SQLite (`storetest.New`) over mocks for storage.

**Shutdown.** Long-running goroutines take a context and exit on cancel; background writers (access log batching) flush on shutdown within `platform.ShutdownTimeout`. Close what you open, in reverse order.

Finish with `pnpm check`.

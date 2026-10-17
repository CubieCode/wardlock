# Architecture

Wardlock is one Go process, `wardlockd`, with an embedded dashboard and a SQLite database, plus a Go CLI that talks to it over the HTTP API. This document records the decisions later items build on. Sections marked with a roadmap ID describe planned behaviour; the rest is built.

```mermaid
flowchart LR
  subgraph clients
    B[Browser]
    A[AI fetchers, curl, preview bots]
    C[wardlock CLI]
  end
  subgraph wardlockd
    E[edge: host routing]
    D[dashboard: embedded SPA]
    API[api: generated OpenAPI handlers]
    ACC[access]
    DEP[deploys / sites / owners]
    ST[store: repository interfaces]
  end
  DB[(SQLite, WAL)]
  FS[(blob files)]
  B --> E
  A --> E
  C -->|apiclient-go| E
  E -->|dashboard host| D
  E -->|dashboard host /api/| API
  E -->|site host| ACC
  API --> DEP
  ACC --> ST
  DEP --> ST
  ST --> DB
  DEP --> FS
  E --> FS
```

## Processes and binaries

- `wardlockd serve` runs everything: the edge on `WARDLOCK_HTTP_ADDR` (and `WARDLOCK_HTTPS_ADDR` from C1.4), the owner API and the dashboard. Configuration is environment variables only (`.env.example` lists every one).
- `wardlock` (CLI) is a separate Go module with no server code; it uses `wardlock.nz/apiclient`.
- `cmd/wardlockd/serve.go` is the only place that wires packages together. It mounts the API under `/api/` on the dashboard host and hands the edge the resulting `http.Handler`, so the edge never imports the API.

## Request flow through the edge

```mermaid
flowchart TD
  R[Request] --> H{path = /healthz?}
  H -->|yes| OK[200 health]
  H -->|no| N[canonical host: lowercase, no port, no trailing dot]
  N --> DH{dashboard host?}
  DH -->|yes| DASH[/api/* to API, else dashboard SPA/]
  DH -->|no| S{site for host? <br/> name.base or verified custom domain}
  S -->|no| NF[404 No site]
  S -->|yes| RES{path starts /.wardlock/?}
  RES -->|yes| RP[reserved handler: health, login, callback, logout, view]
  RES -->|no| AD[active deploy manifest, cached in memory]
  AD --> P[policy: longest matching path prefix]
  P --> CHK{public, or token / session / Bearer valid?}
  CHK -->|no| LG[login page or 401/404]
  CHK -->|yes| F[resolve file: index, clean URL, 404.html, SPA fallback, ?format]
  F --> OUT[serve blob with ETag, Range, precompression, headers]
  CHK -.-> LOG[access log buffer, batched writes]
```

F0.1 implements health, host routing, the dashboard, the built-in hello site and `/.wardlock/health`. The hello site is embedded in `internal/edge/hello` and is removed when C1.3 serves real deploys. The edge's manifest and policy caches are invalidated through a `Notifier` (in-process in v1).

## Deploy upload protocol (C1.2)

```mermaid
sequenceDiagram
  participant CLI as wardlock
  participant API as wardlockd API
  participant BS as BlobStore
  CLI->>CLI: walk dir, apply .wardlockignore, sha256 each file
  CLI->>API: POST /api/v0/sites/{site}/deploys {files:[{path,sha256,size}]}
  API->>BS: Has(hash) for each
  API-->>CLI: 201 {deployId, missing:[hash...]}
  loop each missing hash (parallel, bounded)
    CLI->>API: PUT /api/v0/blobs/{sha256} (body = file bytes)
    API->>BS: verify hash, Put
  end
  CLI->>API: POST /api/v0/deploys/{id}/activate
  API->>API: all blobs present? one transaction: set active deploy
  API-->>CLI: 200 {url}
  API->>API: Notifier: invalidate site cache
```

The server verifies each uploaded blob's SHA-256 before storing it and rejects activation if any blob is missing. Activation is a single transaction that moves the site's `active_deploy_id`; rollback is the same operation pointed at an older deploy. Renditions (A2.6) are generated between upload completion and activation and stored as blobs referenced from the manifest.

## Storage layout

```text
$WARDLOCK_DATA_DIR/
  wardlock.db (+ -wal, -shm)   SQLite: all metadata
  blobs/sha256/ab/cdef…        content-addressed file bodies (C1.2)
  certs/                       certmagic storage (C1.4)
```

Blobs are written to a temporary file in the same directory, fsynced, then renamed into place, so a blob either exists completely or not at all. Garbage collection (C1.8) removes blobs no deploy references, after a grace period that covers uploads in progress.

## Database

- SQLite through `modernc.org/sqlite` (pure Go, no CGO, so cross-compiling stays trivial). WAL mode, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL`, transactions begin `IMMEDIATE`.
- **Migrations:** a minimal in-house runner (`internal/store/migrate.go`) applies embedded `apps/server/migrations/NNNN_name.sql` files in order, each in one transaction, recording them in `schema_migrations`. Forward only; files are never edited after merge. Versions must be contiguous. The server refuses to start against a database newer than itself (`ErrDatabaseNewer`). We chose this over goose because the requirement is small (plain ordered SQL), it adds no dependency, and it keeps the SQL identical for a future Postgres runner. Revisit if we need down migrations or Go-coded migrations.
- **Queries:** `sqlc` generates typed Go from `internal/store/queries/*.sql` into `internal/store/sqlcgen` (committed). Queries use `sqlc.arg(name)` parameters. There is no ORM.
- **Repository interfaces:** `store` declares one interface per area (`Sites`, `Deploys`, `Links`, `Sessions`, `AccessLog`, `Owners`, `Policies`; F0.3 onwards) and implements them with sqlc. Domain packages accept the interfaces only; depguard forbids `database/sql` and `sqlcgen` there. Each interface has a contract test suite run against SQLite through `storetest.New`.

### Data model (F0.3)

```mermaid
erDiagram
  owners ||--o{ owner_sessions : has
  owners ||--o{ api_tokens : has
  owners ||--o{ sites : owns
  sites ||--o{ domains : "custom domains"
  sites ||--o{ deploys : has
  sites ||--o| deploys : "active_deploy_id"
  deploys ||--o{ deploy_files : manifest
  blobs ||--o{ deploy_files : "sha256"
  sites ||--o{ policies : "default + path prefixes"
  sites ||--o{ share_links : has
  share_links ||--o{ site_sessions : creates
  sites ||--o{ access_log : records
```

Columns follow the portable-SQL rules: text IDs (UUIDv7) generated in Go, timestamps as UTC Unix milliseconds in `INTEGER` (Postgres `BIGINT`), booleans as `INTEGER` with `CHECK (x IN (0, 1))`, upserts with `ON CONFLICT … DO UPDATE`. Token and password columns hold only hashes. `access_log` has its own table with no foreign keys out of it, so it can later move to a separate store.

### Storage seam

Wardlock scales up on one machine first: the edge caches manifests in memory, deploys are rare writes, and SQLite in WAL mode handles that easily. Running several server instances would need Postgres, shared blob storage (S3), shared certificate storage and shared rate-limit state, so the seam covers all four:

| Concern            | Interface                                    | v1 implementation      | Later                                     |
| ------------------ | -------------------------------------------- | ---------------------- | ----------------------------------------- |
| Metadata           | `store.Sites`, `Deploys`, `Links`, …         | SQLite + sqlc          | Postgres, passing the same contract tests |
| File bodies        | `BlobStore` (`Has`, `Put`, `Open`, `Delete`) | filesystem             | S3-compatible                             |
| Certificates       | certmagic `Storage` from config              | certmagic file storage | shared storage                            |
| Rate limits        | `Limiter`                                    | in-memory              | shared store                              |
| Cache invalidation | `Notifier`                                   | in-process             | Postgres `LISTEN/NOTIFY` or similar       |

Access-log writes are the only per-request writes. They go into an in-memory buffer flushed in batches (every second or 500 entries) off the request path, so serving never waits on the database; a full buffer drops entries and counts the drops rather than blocking. A Postgres driver, S3 client and multi-instance coordination are out of scope for v1.

## TLS modes (C1.4)

| Mode           | When                                           | Behaviour                                                                                                                                                                                                                             |
| -------------- | ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Automatic      | Public server with ports 80/443                | certmagic obtains a wildcard `*.<base domain>` certificate through DNS-01 (Cloudflare first, then one more `libdns` provider) plus dashboard and auth hosts; on-demand TLS for verified custom domains only. HTTP redirects to HTTPS. |
| Behind a proxy | Caddy, nginx or a load balancer terminates TLS | Plain HTTP only; trusts `X-Forwarded-*` from configured proxy addresses.                                                                                                                                                              |
| Local          | `*.localhost` development                      | Plain HTTP. Browsers treat `http://*.localhost` as a secure context, so `Secure` and `__Host-` cookies still work.                                                                                                                    |

F0.1 runs the local mode only.

## Auth origin (A2.5)

OAuth and OIDC providers cannot register wildcard redirect URIs, so every provider redirect goes to `https://auth.<base domain>/callback/<provider>`, served by the edge. It validates `state` and PKCE, checks the allowlist, then creates a single-use, 60-second code bound to the target site and path and redirects to `https://<site>/.wardlock/callback?code=…`. The edge on the site host exchanges the code server-side and sets the host-only site cookie. No cookie is ever set on the base domain.

## Owner API and code generation

```mermaid
flowchart LR
  SPEC[packages/api-spec/openapi.yaml] -->|oapi-codegen| SRV[apps/server/internal/api/api.gen.go]
  SPEC -->|oapi-codegen| GO[packages/apiclient-go/client.gen.go]
  SPEC -->|openapi-typescript| TS[packages/apiclient-ts/src/schema.gen.ts]
  MIG[apps/server/migrations/*.sql] -->|sqlc| SQLC[internal/store/sqlcgen]
  Q[internal/store/queries/*.sql] -->|sqlc| SQLC
```

- The OpenAPI 3.1 document is the single source of truth. The server implements oapi-codegen's strict interface (`internal/api`); the CLI uses the generated Go client; the dashboard uses `openapi-fetch` typed by `openapi-typescript`. All API paths sit under `/api/v0/` on the dashboard host.
- `pnpm codegen` (`scripts/codegen.sh`) regenerates everything and prepends SPDX headers. `pnpm check:codegen` regenerates and fails if any generated file changed, which is how CI catches hand edits and forgotten regeneration.
- Tool versions are pinned in `tools/go.mod` (sqlc, oapi-codegen, golangci-lint, govulncheck) and built into `.local/bin` on first use; `openapi-typescript` is pinned in `packages/api-spec/package.json`.
- The dashboard is built by Vite to `apps/web/dist`, copied by `scripts/embed-dashboard.sh` into `apps/server/internal/dashboard/ui/dist` (gitignored) and embedded with `go:embed`. A server built without it serves `ui/fallback.html`. In `pnpm dev` the server proxies dashboard pages to Vite instead.

## Go workspace

`go.work` lists `apps/server`, `apps/cli` and `packages/apiclient-go` and pins the toolchain (`go1.27.2`). Module paths are vanity paths on `wardlock.nz`. Because that domain does not yet serve `go-import` metadata, the server and CLI `go.mod` files `replace wardlock.nz/apiclient => ../../packages/apiclient-go`: `go.work` covers workspace builds, and the `replace` covers `GOWORK=off` builds and `go mod tidy`. (A `replace` in `go.work` itself is rejected by Go for a module the workspace also uses.) `tools/` is deliberately outside the workspace so linter dependencies never affect product builds.

## Checks

`pnpm check` runs, in order: ESLint and Prettier; TypeScript typecheck; Vitest; the Turbo build and `scripts/build-go.sh`; `scripts/check-boundaries.mjs` (SPDX headers, package licences, MIT modules not requiring the server); the codegen drift check; then `scripts/check-go.sh` for each workspace module (gofmt, `go vet`, golangci-lint with depguard boundaries, `go test -race`, govulncheck). Go runs from a script rather than through Turborepo because Go's own build and test caches already make reruns cheap, and wrapping each module in a `package.json` would duplicate them. `pnpm browser:test` runs separately because it needs a browser.

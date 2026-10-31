# Modules and boundaries

Wardlock is a modular monolith: one server process, one dashboard, one CLI, with boundaries enforced by folder, Go module, licence and lint rules rather than by separate services. This file says what each module owns and what it may import. "Domain packages" means `edge`, `access`, `oauth`, `deploys`, `sites`, `owners`, `mail`, `moderation`, `country`, `tls` and `api`. Change a rule here and in `.golangci.yml` together.

<details>
<summary>How the rules are enforced</summary>

`pnpm check` enforces the licence boundaries and the TypeScript import rules (`scripts/check-boundaries.mjs`, `eslint.config.js`), and depguard (`.golangci.yml`) enforces every "Must not import" rule below, the MIT modules and the storage rule for every domain package.

</details>

## Go modules

| Module                  | Path                    | Licence       | May import                                                               |
| ----------------------- | ----------------------- | ------------- | ------------------------------------------------------------------------ |
| `wardlock.nz/server`    | `apps/server`           | AGPL-3.0-only | standard library, approved third-party packages, `wardlock.nz/apiclient` |
| `wardlock.nz/cli`       | `apps/cli`              | MIT           | standard library, approved third-party packages, `wardlock.nz/apiclient` |
| `wardlock.nz/apiclient` | `packages/apiclient-go` | MIT           | standard library, `oapi-codegen` runtime                                 |

MIT modules never import AGPL code: depguard denies `wardlock.nz/server` in `apps/cli` and `packages/apiclient-go`, and the boundary script rejects an MIT `go.mod` that requires it.

## Server packages (`apps/server/internal`)

| Package      | Owns                                                                                                                                                                                                                | Must not import                                                         |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `platform`   | Config, logging and redaction, request logging, health and readiness, metrics exposition, graceful shutdown, version                                                                                                | any other internal package                                              |
| `store`      | SQLite connection, migration runner, sqlc queries, repository interfaces and their SQLite implementations, `storetest`                                                                                              | domain packages, `dashboard`                                            |
| `edge`       | Host routing, static serving, policy enforcement at request time, `/.wardlock/*`, response headers, renditions selection                                                                                            | `api`, `store`, `deploys`, `database/sql`, `sqlcgen`, SQLite driver     |
| `access`     | Policies and their evaluation, share links and tokens, site sessions and cookie primitives, password, email code, OAuth, the access-log pipeline (`access/accesslog`)                                               | `net/http`, `api`, `edge`, `database/sql`, `sqlcgen`                    |
| `oauth`      | OAuth and OIDC provider clients (GitHub, Google, generic OIDC): authorisation URLs, code exchange, ID-token checks; `oauthtest` fake                                                                                | `api`, `edge`, `store`, `database/sql`, `sqlcgen`                       |
| `deploys`    | Upload sessions, manifests, `BlobStore`, activation, rollback, garbage collection, rendition generation                                                                                                             | `api`, `edge`, `database/sql`, `sqlcgen`                                |
| `sites`      | Sites, names, custom domains and verification; groups, tags and pins (V5.3)                                                                                                                                         | `api`, `edge`, `database/sql`, `sqlcgen`                                |
| `owners`     | Owner accounts, dashboard sessions, email-code and passkey sign-in, API tokens, CLI login                                                                                                                           | `edge`, `database/sql`, `sqlcgen`                                       |
| `mail`       | `Mailer` interface, SMTP delivery (STARTTLS or implicit TLS), the `dev://` sink, sign-in and email-code templates                                                                                                   | `api`, `edge`, `database/sql`, `sqlcgen`                                |
| `moderation` | Content screening (A2.15): the `Moderator` interface, the OpenAI client, the screening worker and its notices, operator decisions, the `Quarantine` the edge consults; `moderationtest` fake, `testimages` fixtures | `api`, `edge`, `database/sql`, `sqlcgen`                                |
| `notify`     | Owner notifications (I6.2) and webhooks (I6.3): settings, the durable outbox and its worker, what each email says, signing, the guarded dialler. Other packages reach it through small interfaces they declare      | `edge`, `database/sql`, `sqlcgen`                                       |
| `country`    | Country lookup from the operator's local IP-to-country MMDB file (V5.13): loading, checking and rereading it when it changes, ISO 3166-1 alpha-2 codes only; `countrytest` writes synthetic databases               | `net/http`, `api`, `edge`, `access`, `store`, `database/sql`, `sqlcgen` |
| `tls`        | certmagic configuration and modes, the on-demand decision, HTTP to HTTPS redirect, trusted-proxy `X-Forwarded-*` handling                                                                                           | `api`, `edge`, `database/sql`, `sqlcgen`                                |
| `api`        | Generated OpenAPI interface, handlers that translate HTTP to domain calls, error-to-status mapping                                                                                                                  | `edge`                                                                  |
| `dashboard`  | Embedded dashboard files, SPA fallback, dev proxy                                                                                                                                                                   | domain packages, `store`                                                |

Rules of thumb:

- `access` decides; `edge` enforces. The edge extracts plain values from the request (host, path, token string, cookie value, client IP, method) and calls `access`; `access` returns a plain decision (allow with session to set, show login, deny). This keeps access testable without HTTP and keeps HTTP details out of security logic.
- Domain packages declare the small interfaces they need (or take `store`'s repository interfaces) and never see SQL.
- Only `api` maps domain sentinel errors to HTTP status codes for the owner API; only `edge` does so for site responses.
- `cmd/wardlockd` is the composition root and the only package that imports both `edge` and `api`.

## TypeScript packages

| Package                  | Path                    | Licence       | May import                                         |
| ------------------------ | ----------------------- | ------------- | -------------------------------------------------- |
| `@wardlock/web`          | `apps/web`              | AGPL-3.0-only | `@wardlock/ui`, `@wardlock/apiclient-ts`, React    |
| `@wardlock/ui`           | `packages/ui`           | AGPL-3.0-only | React only; no `@wardlock/*` packages, no app code |
| `@wardlock/apiclient-ts` | `packages/apiclient-ts` | MIT           | `openapi-fetch`; never AGPL packages               |
| `@wardlock/api-spec`     | `packages/api-spec`     | MIT           | (tooling only)                                     |
| `@wardlock/docs-site`    | `docs-site`             | MIT           | `marked`; never AGPL packages                      |

The dashboard calls the server only through `@wardlock/apiclient-ts`: ESLint forbids `fetch`, `XMLHttpRequest`, `EventSource`, `WebSocket` and importing `openapi-fetch` directly in `apps/web/src`. Workspace packages are imported by name, never by relative path across package boundaries.

## Generated code

Generated files are committed, start with a "Code generated … DO NOT EDIT" or equivalent notice, and are never edited by hand. Change the source and run `pnpm codegen`.

<details>
<summary>Generated files, generators and sources</summary>

| File                                                                      | Generator                                   | Source                                               |
| ------------------------------------------------------------------------- | ------------------------------------------- | ---------------------------------------------------- |
| `apps/server/internal/api/api.gen.go`                                     | oapi-codegen (strict server, models)        | `packages/api-spec/openapi.yaml`                     |
| `apps/server/internal/api/unimplemented.gen.go`, `operations.gen_test.go` | oapi-codegen with `internal/api/templates/` | `packages/api-spec/openapi.yaml`                     |
| `packages/apiclient-go/client.gen.go`                                     | oapi-codegen (client, models)               | `packages/api-spec/openapi.yaml`                     |
| `packages/apiclient-ts/src/schema.gen.ts`                                 | openapi-typescript                          | `packages/api-spec/openapi.yaml`                     |
| `apps/server/internal/store/sqlcgen/*.go`                                 | sqlc                                        | `apps/server/migrations/`, `internal/store/queries/` |

</details>

## Serialization gates

Lanes that run in parallel must not edit these at the same time; one owner per wave:

- `apps/server/migrations/` and `internal/store/queries/` schema-affecting changes (the migration owner). Query files are per area, so new queries in different files can proceed in parallel once the schema is merged.
- `packages/api-spec/openapi.yaml` and the generated bindings.
- `packages/ui`.
- Cookie and session primitives in `internal/access`.
- `pnpm-lock.yaml` and each `go.sum` (the lockfile owner).

## Adding a package

A new server package needs a row in the table above, depguard rules if it has forbidden imports, an SPDX header in every file and a doc comment saying what it owns. Prefer extending an existing package over creating one; never add a package for a feature that has not been assigned.

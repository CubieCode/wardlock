# Agent conventions

Read README.md, docs/SPEC.md, docs/ARCHITECTURE.md, docs/MODULES.md and the target ROADMAP.md item. Wardlock is a self-hostable static site host with built-in access control: one Go server (`wardlockd`) with an embedded React dashboard, a Go CLI (`wardlock`) and SQLite. Every feature, including each roadmap item, needs an explicit implementation assignment.

Naming: display "Wardlock"; identifiers, package scopes, environment variables, cookies and branches use `wardlock`/`WARDLOCK_`. The project's only domain is `wardlock.nz`; Go modules use the vanity paths `wardlock.nz/server`, `wardlock.nz/cli` and `wardlock.nz/apiclient`. Prose is NZ English (behaviour, colour, licence as a noun).

## Structure and commands

Canonical skills are `.agents/skills/`; `.claude/skills` is a symlink to `../.agents/skills`. CLAUDE.md references this file. Vendored skills are recorded in `skills-lock.json`.

- `apps/server` (AGPL, Go module `wardlock.nz/server`): `cmd/wardlockd` wires everything; `internal/edge` (host routing, static serving, access enforcement, `/.wardlock/*`), `internal/access` (policies, share links, site sessions, password, email code, OAuth), `internal/deploys`, `internal/sites`, `internal/owners` (dashboard/API auth), `internal/tls`, `internal/store` (SQLite, migrations runner, sqlc), `internal/api` (generated OpenAPI handlers), `internal/platform` (config, logging, health, shutdown), `internal/dashboard` (embedded dashboard); `migrations/`.
- `apps/cli` (MIT, `wardlock.nz/cli`): the `wardlock` binary. Imports only `wardlock.nz/apiclient`.
- `apps/web` (AGPL): React + Vite dashboard, built to static files and embedded in `wardlockd`.
- `packages/api-spec` (MIT): `openapi.yaml`, the single source of truth for the API, and `generate.sh`.
- `packages/apiclient-go` (MIT, `wardlock.nz/apiclient`) and `packages/apiclient-ts` (MIT): generated clients.
- `packages/ui` (AGPL): domain-free design system (tokens, components). All dashboard screens use it.
- `deploy/` (Docker, compose, systemd, reverse-proxy examples), `fixtures/` (synthetic sites), `scripts/`, `tools/` (pinned Go tool versions).

Boundaries (docs/MODULES.md), enforced by `.golangci.yml` (depguard), `eslint.config.js` and `scripts/check-boundaries.mjs`:

- `internal/edge` never imports `internal/api`. `internal/access` never imports `net/http`, `internal/api` or `internal/edge`; it receives plain values.
- Domain packages depend on `store`'s repository interfaces, never `database/sql`, `sqlcgen` or the SQLite driver.
- The CLI imports only `packages/apiclient-go`, never `apps/server`. The server may import MIT client packages; MIT code never imports AGPL code.
- The dashboard reaches the server only through `@wardlock/apiclient-ts`.
- Generated code (`*.gen.go`, `*.gen.ts`, `internal/store/sqlcgen/`) is committed, marked generated and never edited by hand. `pnpm check` fails if regenerating it produces a diff.

Commands:

- `pnpm install --frozen-lockfile`, then `pnpm check`: the definition-of-done gate. It runs ESLint and Prettier, the ROADMAP board check, typecheck, Vitest, the TS and Go builds, the licence/boundary check, the codegen drift check, and for every Go module gofmt, `go vet`, golangci-lint, `go test -race` and govulncheck (`scripts/check-go.sh`). The first run builds the pinned Go tools into `.local/bin`.
- `pnpm dev`: wardlockd plus the dashboard's Vite dev server, proxied through wardlockd, on this lane's `.env` ports with data in `.local/data`.
- `pnpm codegen` after editing `openapi.yaml`, a migration or a query. `pnpm format` before final checks. `pnpm build` builds the dashboard and the binaries into `.local/bin`. `pnpm browser:test` runs Playwright acceptance against a real server (install the browser once: `pnpm --filter @wardlock/web exec playwright install chromium`).
- `pnpm roadmap:board` after changing any Status or PR line in ROADMAP.md: it regenerates the status board and folds finished items. `pnpm check` runs it with `--check` and fails while the board is stale.
- Go tests sit next to code as `*_test.go`; TS tests as `*.test.ts(x)`; browser specs under `apps/web/test/`.

## Coding and data

Go: follow [wardlock-go-service](.agents/skills/wardlock-go-service/SKILL.md): context first, explicit dependencies, wrapped errors with sentinels mapped to HTTP only at the boundary, `log/slog` with redaction, portable SQL behind repository interfaces, table-driven and `httptest` tests. TypeScript: strict, ESM, `.js` import suffixes in packages. No speculative packages, abstractions or dependencies; a dependency change updates the lockfile or `go.sum` and is made by the wave's lockfile owner.

Anything touching `internal/edge`, `internal/access`, tokens, cookies or sessions follows [wardlock-access](.agents/skills/wardlock-access/SKILL.md) and its test matrix. UI follows [wardlock-web](.agents/skills/wardlock-web/SKILL.md), `@wardlock/ui` and docs/DESIGN-SYSTEM.md; new or changed screens start with Mobbin design research (the skill says how) and cite the references in the PR. Unbuilt features stay invisible in the dashboard and CLI: no placeholder commands, menu items or "coming soon" states.

Every owner-scoped query takes the owner from the verified session or API token, never from the request; missing and foreign resources return the same 404. Never log raw tokens, cookies, passwords, email codes or Authorization headers. `.env`, `.local/` and data directories are gitignored; never copy them, real tokens, real email addresses or customer content into fixtures, tests, logs, screenshots, PRs or public artefacts. Fixture sites are synthetic.

## Docs style

Hand-written Markdown leads each section with what most readers need: a few sentences, a short list or a table. Implementation detail, edge cases, exact limits and their rationale, internals and threat-by-threat reasoning go in a collapsed `<details>` block under that lead, with `<summary>` on its own line, a blank line after `</summary>` and before `</details>`, and headings kept outside it. Never delete something still true to shorten a page; move it into a block. Keep every heading that other docs, the API contract, code or skills link to, or update every link.

## Roadmap and Git

One item, one branch `feat/<id-lower>-<slug>`, one PR titled `<ID>: <title>`. Start only when dependencies are `done` on main. Update only your item's Status, Owner and PR fields, then run `pnpm roadmap:board`. Statuses: `todo`, `in-progress`, `review`, `done`, `blocked` (with reason). `done` means merged and accepted; the coordinator sets it after merge. Report blocking product decisions instead of silently picking contradictory rules.

Use [wardlock-item](.agents/skills/wardlock-item/SKILL.md) for one item; use [wardlock-orchestrate](.agents/skills/wardlock-orchestrate/SKILL.md) only when asked to coordinate lanes. Resolve the Orca executable and read its live CLI guide before running commands; never bare `orca` on unmanaged Linux. One worktree, `.env`, port set and SQLite data directory per lane. Serialization gates, one lane at a time each: migrations and the sqlc schema; `packages/api-spec`; `packages/ui`; `internal/access` cookie and session primitives; lockfiles (`pnpm-lock.yaml`, each `go.sum`). A ready item is not automatically safe to run beside another that edits the same files.

Commit plain, useful messages; no AI attribution, no `--no-verify`, no force-push to main. The PR body explains the result, validation, unresolved decisions and any visual gate; use `gh pr create --body-file` with real newlines. Merge only when the task authorises it, checks pass and review and visual gates are satisfied. Never publish packages or images, change DNS, register domains, change repository settings or deploy without an explicit assignment.

## Definition of done

Acceptance criteria met within scope; meaningful tests added and passing; `pnpm check` and `git diff --check` pass; generated code regenerated and committed; docs and the API contract describe the new behaviour; any migration is portable SQL with contract tests; owner-isolation tests where resources are owner-scoped. Access changes pass the full `wardlock-access` test matrix. UI changes cite their Mobbin design references and are checked in a browser at desktop (1440px) and mobile (390px) widths in light and dark. No secrets or tokens in fixtures, logs, screenshots or PRs; no build output staged. Reviewed PR merged, then the roadmap marked `done`. State unavailable checks honestly; never mark an untested gate complete.

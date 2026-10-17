# Roadmap

Statuses are `todo`, `in-progress`, `review`, `done` and `blocked` (with a reason). `done` means merged to main and accepted, not merely coded; the coordinator sets it after merge. Implementing any item needs an explicit assignment; a request to scaffold, document or plan is not one. Unbuilt features stay invisible in the dashboard and CLI until their item is done.

Each item is one focused PR on branch `feat/<id-lower>-<slug>`, implemented with the [wardlock-item](.agents/skills/wardlock-item/SKILL.md) skill; parallel lanes are coordinated with [wardlock-orchestrate](.agents/skills/wardlock-orchestrate/SKILL.md). Start an item only when every dependency is `done` on main. Workers update only their own item's Status, Owner and PR fields. The **Parallel** line names the files an item owns, the serialization gates it touches (migrations and sqlc schema, `packages/api-spec`, `packages/ui`, `internal/access` cookie/session primitives, lockfiles) and the items it can share a wave with; check real file overlap before launching. One migration owner and one lockfile owner per wave.

## F0: Foundation

### F0.1: Monorepo scaffold

- Status: `done`
- Owner: feat/f0.1-monorepo-scaffold
- PR: https://github.com/CubieCode/wardlock/pull/1
- Depends on: none
- Scope: pnpm + Turborepo workspace and Go workspace (`go.work`); tooling (ESLint, Prettier, Vitest, Playwright, golangci-lint, govulncheck, sqlc, oapi-codegen pinned in `tools/`); `pnpm check` and `pnpm dev`; CI and snapshot-only release workflow with GoReleaser; licences and SPDX headers; README, AGENTS, SPEC, ARCHITECTURE, MODULES, SECURITY-MODEL, DEPLOYMENT, DESIGN-SYSTEM, DESIGN, CONTRIBUTING, SECURITY; skills; Orca setup; hello-world server, CLI and dashboard wired end to end (`GET /api/v0/health` through generated Go and TS clients).
- Acceptance: `pnpm install --frozen-lockfile && pnpm check` passes from a clean clone; `scripts/worktree-setup.sh` works in a second worktree with different ports and no shared state; `wardlockd serve` serves the embedded dashboard (a `packages/ui` placeholder) on the dashboard host, the hello-world site at `hello.wardlock.localhost` and `/healthz`; `wardlock version` and `--help` work and list only built commands; codegen drift check runs in CI; no secrets committed; `.local/`, `.env` and data are ignored.
- Skills: wardlock-item
- Parallel: Scaffold; runs alone.

### F0.2: API contract v0 and generation pipeline

- Status: `done`
- Owner: `feat/f0.2-api-contract`
- PR: https://github.com/CubieCode/wardlock/pull/14
- Depends on: F0.1
- Scope: Extend `packages/api-spec/openapi.yaml` with every C1 endpoint so C1 lanes can build in parallel against an agreed contract: shared error schema (`code`, `message`) and status conventions; security schemes (dashboard session cookie, `Bearer` API token); owner bootstrap and email-code sign-in, sessions, sign-out; API tokens and the CLI device-login flow; sites (create, list, get, delete); deploys (create with manifest returning missing hashes, upload blob, activate, list, rollback); custom domains (add, list, get, verify, remove; responses carry status, the routing and TXT records to create, last check time and failure reason, per SPEC "Custom domains"). Pagination and ID conventions. Add an OpenAPI lint step (for example Redocly CLI or vacuum, pinned) to `pnpm check`. The server keeps returning 501 for operations not yet implemented, through a generated-interface stub, and `--help`/UI expose none of them.
- Acceptance: The spec validates and lints clean; Go server, Go client and TS client regenerate with no hand edits and compile; drift check passes; every operation has an `operationId`, error responses, examples and the security it needs; the 501 stub has a test proving every unimplemented operation answers 501 rather than 404; ARCHITECTURE's upload protocol and SPEC match the spec.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 1 with F0.3, F0.4 and O4.1. Sole owner of `packages/api-spec`, `internal/api` and the generated clients (gate). Lockfile owner only if it adds the spec linter.

### F0.3: Data model and migrations v0

- Status: `done`
- Owner: feat/f0.3-data-model-migrations
- PR: https://github.com/CubieCode/wardlock/pull/12
- Depends on: F0.1
- Scope: Migration `0002` onward for owners, owner sessions, API tokens, CLI login requests, sites, domains (hostname unique per instance, site, status `pending`/`active`/`lapsed`, verification token (shown again in the TXT record, so stored as is; it is public once published in DNS), last check, first consecutive failure, last failure reason), deploys, blobs, deploy files (manifests), policies, share links, site sessions and the access log, following the portable-SQL rules. Declare the per-area repository interfaces in `internal/store` (`Owners`, `Sessions`, `APITokens`, `Sites`, `Domains`, `Deploys`, `Policies`, `Links`, `AccessLog`) with Go types that hide sqlc; an ID helper (UUIDv7). Implement only what contract tests need to exercise constraints; feature items implement the remaining methods in their own `queries/<area>.sql`. Contract-test harness that each interface's suite plugs into.
- Acceptance: Migrations apply to an empty database and are idempotent on restart; foreign keys, uniqueness (site name, domain, token public ID) and `CHECK` constraints are tested; no `AUTOINCREMENT`, `INSERT OR REPLACE` or SQLite-only functions (a test greps migrations and queries); timestamps are Unix ms and IDs are text; ARCHITECTURE's data model diagram matches the schema; contract test harness runs under `go test -race`.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 1 with F0.2, F0.4 and O4.1. Migration owner and lockfile owner (server `go.sum`) for the wave. Owns `apps/server/migrations/`, `internal/store/` (except `sqlcgen` output it regenerates).

### F0.4: Mail delivery and dev mail sink

- Status: `done`
- Owner: `feat/f0.4-mail-delivery`
- PR: https://github.com/CubieCode/wardlock/pull/10
- Depends on: F0.1
- Scope: `internal/mail` with a `Mailer` interface; `WARDLOCK_SMTP_URL` parsing in `platform` config (`smtp://` with required STARTTLS, `smtps://`, `dev://`); the `dev://` sink that logs each message (recipient and subject, never codes in production modes) to the console and writes it to `.local/mail/` as `.eml`; plain-text and HTML templates for sign-in and email-code messages. Split out of A2.4 because owner sign-in (C1.1) needs mail first.
- Acceptance: Unit tests cover URL parsing and rejection of plain `smtp://` without STARTTLS; an SMTP test against an in-process fake server sends a message; the dev sink writes readable `.eml` files and needs no Docker; `.env.example` and DEPLOYMENT updated; mail errors never include credentials.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 1 with F0.2, F0.3 and O4.1. Owns `internal/mail/` and the SMTP lines of `internal/platform/config.go`. Uses only the standard library (`net/smtp` or a minimal client); not a lockfile owner.

## C1: Core hosting

### C1.1: Owner bootstrap, email-code sign-in and dashboard sessions

- Status: `done`
- Owner: `feat/c1.1-owner-sign-in`
- PR: https://github.com/CubieCode/wardlock/pull/18
- Depends on: F0.2, F0.3, F0.4
- Scope: `internal/owners`: one-time setup URL printed on first start when no owners exist; `wardlockd admin create --email`; inviting further owners; email-code sign-in (6 digits, 10 minutes, 5 attempts, rate-limited); dashboard sessions as `__Host-` cookies with idle and absolute expiry; sign-out and session revocation; middleware deriving owner context from the session for `internal/api`; implement the C1.1 API operations.
- Acceptance: Integration tests through the real HTTP handler cover setup URL single use and expiry, admin create, code expiry, wrong code, attempt and send rate limits, neutral responses, cookie attributes, sign-out, revoked session; no code or cookie value appears in logs (asserted); `wardlockd --help` lists `admin create`.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 2 with C1.2, C1.3 and C1.4. Owns `internal/owners/`, owner queries and `cmd/wardlockd` admin subcommand. Touches dashboard cookie primitives (gate shared with nothing else in wave 2). No migrations (F0.3 created the tables).

### C1.2: Deploy API

- Status: `done`
- Owner: `feat/c1.2-deploy-api`
- PR: https://github.com/CubieCode/wardlock/pull/19
- Depends on: F0.2, F0.3
- Scope: `internal/deploys` and `internal/sites`: filesystem `BlobStore` (temp file, fsync, rename; SHA-256 verified on upload); create deploy from manifest returning missing hashes; blob upload; atomic activation; list, rollback and teardown; path validation (no `..`, control characters or `.wardlock/`); configurable size and count limits; site creation on first deploy with reserved-name checks; `Notifier` interface with an in-process implementation fired on activation. Implement the `Sites`, `Deploys` and blob repository methods.
- Acceptance: Tests cover an unchanged re-deploy uploading nothing, a hash mismatch rejected, activation failing with a missing blob, concurrent activations, rollback to any earlier deploy, teardown, every limit, every rejected path form, and reserved names; two-owner isolation (owner B cannot see, deploy to, roll back or tear down owner A's site); contract tests for every new repository method.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 2 with C1.1, C1.3 and C1.4. Owns `internal/deploys/`, `internal/sites/`, sites/deploys queries. Defines `BlobStore` and `Notifier`, which C1.3 consumes: agree their method sets in both briefs before starting.

### C1.3: Edge static serving

- Status: `done`
- Owner: feat/c1.3-edge-serving
- PR: https://github.com/CubieCode/wardlock/pull/21
- Depends on: F0.3
- Scope: Replace the built-in hello site with real serving: host to site lookup (base-domain names and verified custom domains), active manifest cache invalidated by `Notifier`, index and clean URLs, `404.html`, optional SPA fallback, `ETag`/`Last-Modified` and conditional requests, `Range`, precompressed `.br`/`.gz` siblings, cache headers, `/.wardlock/` reserved before deploy content. Policies are not enforced yet (all sites public until A2.1); the edge calls an access hook that allows everything.
- Acceptance: Table-driven `httptest` suite over `fixtures/sites/` covering every rule in SPEC "Serving"; a deploy file named `.wardlock/health` is never served; cache invalidation test after activation; benchmark for a cached small file recorded in the PR; hello site removed from code and docs.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 2 with C1.1, C1.2 and C1.4. Owns `internal/edge/` and `fixtures/sites/`. Consumes `BlobStore`/`Notifier` from C1.2 through interfaces with in-test fakes; final wiring in `cmd/wardlockd` after C1.2 merges (merge C1.2 first).

### C1.4: TLS

- Status: `done`
- Owner: `feat/c1.4-tls`
- PR: https://github.com/CubieCode/wardlock/pull/27
- Depends on: F0.1
- Scope: `internal/tls` with certmagic: wildcard certificate for `*.<base domain>` plus dashboard and auth hosts through DNS-01 with Cloudflare and one more `libdns` provider; on-demand TLS restricted to verified custom domains (decision function backed by `Domains`); plain-HTTP mode behind a proxy with trusted-proxy configuration for `X-Forwarded-*`; local `*.localhost` mode (today's behaviour); HTTP to HTTPS redirect; certmagic storage under the data directory, passed in as `Storage`.
- Acceptance: Tests with certmagic's test ACME directory or a local Pebble instance (documented; not in `pnpm check` if it needs Docker) cover issuance and the on-demand allow/deny decision; unit tests cover forwarded-header trust; configuration errors are clear; DEPLOYMENT updated; no real DNS credentials anywhere in the repo or CI.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 2 with C1.1, C1.2 and C1.3. Owns `internal/tls/` and TLS config lines. Lockfile owner for wave 2 (certmagic, libdns providers).

### C1.5: CLI deploy and site commands

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.2, C1.7
- Scope: `wardlock deploy`, `list`, `rollback`, `teardown`; `wardlock.json` and `.wardlockignore` (gitignore syntax, documented defaults); parallel bounded uploads with progress output; `--json` output for every command, no prompts without a TTY; `WARDLOCK_TOKEN` for CI; exit codes per SPEC.
- Acceptance: Tests against an in-process `wardlockd` (or the generated client's test server) cover a first deploy, a no-change deploy, ignore rules, every `wardlock.json` field and unknown-field rejection, rollback, teardown confirmation, `--json` schemas (documented), and network failure messages; `--help` lists exactly the built commands; works on Linux, macOS and Windows path separators (unit tests).
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 4 with A2.2, D3.2 and C1.8. Owns `apps/cli/`. No server changes.

### C1.6: Owner passkeys

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.1
- Scope: WebAuthn passkey registration and sign-in for owners (relying party = dashboard host), listing and removing passkeys; dashboard sign-in screen offers passkeys alongside email code once D3.1 exists.
- Acceptance: Ceremony tests with a software authenticator; replayed or cross-origin assertions rejected; two-owner isolation for passkey management; browser test with a virtual authenticator once D3.1 is done.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.7, A2.1 and A2.8. Owns passkey code in `internal/owners/`; lockfile owner for wave 3 (WebAuthn library). May need a migration if F0.3 omitted passkey tables: then it is the wave's migration owner.

### C1.7: API tokens and CLI browser login

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.1
- Scope: Scoped, named, revocable API tokens (`deploy`, `manage`) in the shared token format; `Bearer` authentication for the API; device-code CLI login (`wardlock login`, `logout`), approval page on the dashboard host; token storage in the OS keychain with a 0600 file fallback; `WARDLOCK_TOKEN` override.
- Acceptance: Tests cover token hashing and constant-time compare, scope enforcement on every C1 operation, revocation, expired login requests, polling rate limits, keychain fallback file permissions; tokens never logged (asserted); `wardlock login`/`logout` appear in `--help`.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.6, A2.1 and A2.8. Owns token code in `internal/owners/` (coordinate file split with C1.6 in the briefs) and `apps/cli` login files. Adds the keychain library only via the wave's lockfile owner.

### C1.8: Blob garbage collection

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.2
- Scope: Periodic and on-demand (`wardlockd gc`) removal of blobs referenced by no deploy, with a grace period covering in-flight uploads; pruning deploy history beyond the configured limit (never the active deploy); metrics of bytes freed in logs.
- Acceptance: Tests prove referenced blobs and blobs younger than the grace period survive, concurrent upload plus GC never loses a blob needed by activation, and history pruning keeps the active deploy.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 4 with C1.5, A2.2 and D3.2. Owns GC files in `internal/deploys/`.

### C1.9: Custom domains

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.3, C1.4, C1.5
- Scope: Custom domains per SPEC "Custom domains": hostname normalisation and rejection rules in `internal/sites`; add, list, get, verify and remove behind the F0.2 operations; a `Resolver` interface (CNAME chain, TXT) with a `net.Resolver` implementation honouring `WARDLOCK_DNS_RESOLVERS` and an in-test fake; a verification worker (initial backoff, 24-hour re-checks, 72-hour lapse, automatic recovery) started from `cmd/wardlockd`; `Notifier` events so the edge and the on-demand TLS decision see status changes immediately; certificate removal on detach; `/.wardlock/tls-ask` for behind-a-proxy mode; `wardlock domain add|list|verify|remove`; the on-demand block in `deploy/Caddyfile.example`; DEPLOYMENT "Custom domains". Implement the `Domains` repository methods.
- Acceptance: Table-driven tests over the fake resolver cover CNAME direct and chained, CNAME to another site or elsewhere, TXT fallback (apex, proxied), wrong and stale tokens, DNS errors and timeouts, backoff timing, lapse after 72 hours and recovery, all with an injected clock; normalisation tests (case, trailing dot, IDN to A-label, IPs, wildcards, base domain, dashboard and auth hosts); `domain_taken` reveals no site or owner; two-owner isolation (owner B cannot list, verify, remove or see owner A's domains: same 404); a re-claim after removal needs a new token; end-to-end `httptest` proving pending and lapsed hosts are not served and the on-demand decision denies them; `tls-ask` answers only trusted-proxy sources in proxy mode; CLI tests including `--json` and `--wait`; contract tests for every new repository method; verification tokens never logged.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.3, A2.4 and A2.6. Owns domain files in `internal/sites/`, `queries/domains.sql`, the domain worker wiring and `WARDLOCK_DNS_RESOLVERS` config lines, `apps/cli` domain commands, `deploy/Caddyfile.example`. Touches `internal/edge` only for `tls-ask` (coordinate with A2.3's login template work in the briefs). Conflicts with O4.2 on `internal/platform` and `cmd/wardlockd`: run O4.2 after it. No migrations or API changes if F0.2 and F0.3 covered domains as scoped; otherwise it is the wave's migration owner and needs the API gate.

## A2: Access control

### A2.1: Policy model and evaluation

- Status: `done`
- Owner: feat/a2.1-policy-model
- PR: https://github.com/CubieCode/wardlock/pull/26
- Depends on: C1.3
- Scope: `internal/access` policy evaluation: site default plus path-prefix policies, longest prefix wins, public or protected with alternative methods; policy versioning; site session cookie primitives (`__Host-`, signed, site- and version-bound); edge integration returning login page, 401 or allow; protected-site response headers; API and repository methods for reading and setting policies; `wardlock policy set` for `--public`/`--protected --links`.
- Acceptance: Unit tests over the decision function cover precedence, method alternatives, version bumps invalidating sessions and cookie tamper/forgery/cross-site use; edge tests prove headers on protected responses and that `/.wardlock/` is unaffected by policies; `access` imports no HTTP packages.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.6, C1.7 and A2.8. Owns `internal/access/` policy and cookie files (gate) and edge enforcement hook.

### A2.2: Share links

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.1, A2.8, C1.7
- Scope: Everything in [wardlock-access](.agents/skills/wardlock-access/SKILL.md) for links: create with label, expiry or never, use limit and path scope; `?t=` and `Authorization: Bearer`; 200 on first request with `Set-Cookie`; HTML-only `history.replaceState` strip; check on every request; revoke and rotate; use-limited interstitial counting uses on POST; access log entries per access; API operations and `wardlock share create|list|revoke|rotate`.
- Acceptance: The full wardlock-access test matrix passes; fuzz test for token parsing; `curl` and a no-JS, no-cookie client get the content on the first request; links never appear raw in logs or the access log (asserted).
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 4 with C1.5, D3.2 and C1.8. Owns link files in `internal/access/`, link queries and `apps/cli` share commands (coordinate `apps/cli` files with C1.5: separate files, C1.5 merges first). API contract additions are a gate: agree them before the wave.

### A2.3: Password policy

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.1
- Scope: Per-policy password (Argon2id), `/.wardlock/login` password form, attempt rate limiting through `Limiter` (in-memory implementation if not yet present), password change ending password sessions, `wardlock policy set --password` reading from prompt or stdin.
- Acceptance: Tests cover correct and wrong passwords, rate limits and reset, session invalidation on change, timing-safe comparison, login page `no-store` and no password in logs.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.4 and A2.6. Owns password files in `internal/access/` and the login page template. Login page shell shared with A2.4: A2.3 lands the shared template first or both agree it in their briefs. Lockfile owner if Argon2 needs `golang.org/x/crypto`.

### A2.4: Email-code policy

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.1, F0.4
- Scope: Email-code method with address and domain allowlists, neutral responses, 6-digit codes, send and verify rate limits, use of the F0.4 mailer and dev sink.
- Acceptance: Tests cover allowlist matching (exact, domain, `*.domain`), neutral responses for disallowed addresses, expiry, attempts, rate limits per IP, address and site, and codes never logged outside the dev sink.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.3 and A2.6. Owns email-code files in `internal/access/`. Shares the login page with A2.3 (see A2.3).

### A2.5: OAuth and OIDC

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.1, C1.4
- Scope: Central auth origin `auth.<base domain>`; GitHub, Google and generic OIDC providers configured by the operator; PKCE, `state`, OIDC `nonce`; single-use, 60-second, site-bound codes exchanged at `/.wardlock/callback`; allowlists by verified email, domain and GitHub organisation.
- Acceptance: Tests against in-process fake providers cover the full flow, replayed and expired codes, wrong-site codes, `state` and PKCE failures, unverified emails and each allowlist type; no cookie is ever set on the base domain.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 6 with D3.3 and D3.4. Owns OAuth files in `internal/access/` and the auth-origin route in `internal/edge/`. Lockfile owner if an OIDC library is added.

### A2.6: AI-readable renditions

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.1, C1.2
- Scope: HTML to Markdown and plain text at deploy time, stored as blobs referenced from the manifest; `?format=md|txt` with correct content types; `Link: rel="alternate"` on HTML; documented conversion contract in SPEC; golden-file fixtures.
- Acceptance: Golden tests over `fixtures/sites/` (headings, lists, tables, code, links without tokens, images' alt text, scripts and styles removed); `?t=…&format=md` works with no cookies and no JavaScript; protected renditions are denied without access; renditions never leak `t` into links.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.3 and A2.4. Owns `internal/deploys/renditions*` and the format selection in `internal/edge/`. Lockfile owner if an HTML parser beyond `golang.org/x/net/html` is needed.

### A2.7: Access security review

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.2, A2.3, A2.4, A2.5, A2.6, D3.3
- Scope: Walk docs/SECURITY-MODEL.md against the implementation; fuzz token parsing and cookie decoding; verify every rate limit; check headers on every protected response type; check log hygiene; fix small findings, record larger ones as new roadmap items.
- Acceptance: A findings table in the PR with each threat's status and evidence; fuzzers committed and run for a recorded duration; no open high-severity finding without an item.
- Skills: wardlock-item, wardlock-access, wardlock-go-service
- Parallel: Runs alone (reads everything; may touch any access file).

### A2.8: Access log pipeline

- Status: `review`
- Owner: `feat/a2.8-access-log`
- PR: https://github.com/CubieCode/wardlock/pull/24
- Depends on: F0.3
- Scope: Buffered, batched access-log writer off the request path (flush every second or 500 entries, drop-and-count when full, flush on shutdown); configurable IP truncation; retention pruning; `AccessLog` repository methods; query by site and link.
- Acceptance: Tests prove the request path never blocks on the database (a slow fake store does not slow serving), counts dropped entries, flushes on shutdown, truncates IPv4 and IPv6 as configured and never stores raw tokens.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.6, C1.7 and A2.1. Owns `internal/access/accesslog*` (or a dedicated package agreed in the brief) and access-log queries.

## D3: Dashboard

### D3.1: Dashboard shell and owner sign-in

- Status: `in-progress`
- Owner: `feat/d3.1-dashboard-shell`
- PR:
- Depends on: C1.1
- Scope: Router, authenticated shell with navigation for built screens only, email-code sign-in and sign-out screens, setup-URL first-owner flow, session expiry handling, design-system additions they need (text field, form errors).
- Acceptance: Browser tests at 1440px and 390px in light and dark cover sign-in, wrong code, expiry and sign-out; component tests for new `packages/ui` components; no navigation to unbuilt screens. Mobbin references cited in the PR (start with: email one-time-code sign-in flow; web app shell with sidebar navigation on desktop and mobile).
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 3 or later, alone among UI lanes (owns `packages/ui` gate and `apps/web/src` shell).

### D3.2: Sites and deploys

- Status: `todo`
- Owner:
- PR:
- Depends on: D3.1, C1.2
- Scope: Site list with policy and last deploy, site detail with deploy history, rollback and teardown with clear confirmations, copyable site URL.
- Acceptance: Browser tests for list, empty state, rollback and teardown at both widths and themes; foreign site IDs show the same not-found state. Mobbin references cited in the PR (start with: hosting dashboard project list; deployment history with rollback; delete-project confirmation).
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 4 with C1.5, A2.2 and C1.8. Owns `apps/web/src/sites/`. `packages/ui` additions agreed with no other UI lane in the wave.

### D3.3: Share links

- Status: `todo`
- Owner:
- PR:
- Depends on: D3.2, A2.2
- Scope: Create a link (label, expiry or never, use limit, path scope), show the URL once with a copy action, list links, revoke, rotate, per-link access log.
- Acceptance: Browser tests cover creation, the one-time display, copy, revoke and rotate confirmations and the access log view; tokens never appear after creation, in page titles or in screenshots. Mobbin references cited in the PR (start with: create share link dialog with expiry and permissions; API key shown once with copy; activity or access log table).
- Skills: wardlock-item, wardlock-web, wardlock-access
- Parallel: Wave 6 with A2.5 and D3.4 (D3.4 owns separate screens; only one may change `packages/ui`).

### D3.4: Policies editor

- Status: `todo`
- Owner:
- PR:
- Depends on: D3.2, A2.3, A2.4
- Scope: View and edit the site default and path policies, choose methods, set a password, manage email allowlists, and OAuth allowlists once A2.5 is done.
- Acceptance: Browser tests for each method and for path policy precedence explained in the UI; passwords never echoed back. Mobbin references cited in the PR (start with: page visibility and access settings; password-protect a page; allowlist of emails and domains).
- Skills: wardlock-item, wardlock-web, wardlock-access
- Parallel: Wave 6 with A2.5 and D3.3. Owns `apps/web/src/policies/`.

### D3.5: Custom domains

- Status: `todo`
- Owner:
- PR:
- Depends on: D3.2, C1.9
- Scope: A Domains section on the site detail screen: add a hostname, show the routing CNAME (and the TXT fallback with when to use it) with copy actions, live status with last check and the failure reason in plain words, "Check now", remove with a confirmation that reminds the owner to delete the DNS record.
- Acceptance: Browser tests at 1440px and 390px in light and dark cover add, each status, a failure reason, "Check now", `domain_taken` and removal; foreign site IDs show the same not-found state; records are copyable exactly as shown. Mobbin references cited in the PR (start with: add custom domain with DNS records to configure; domain verification status pending and verified; copy DNS record value).
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 6 with A2.5, D3.3 and D3.4. Owns `apps/web/src/domains/` and the Domains section's mount point in `apps/web/src/sites/`. Only one of D3.3, D3.4 and D3.5 changes `packages/ui`.

## O4: Operations and release

### O4.1: Release artefacts

- Status: `done`
- Owner: feat/o4.1-release-artefacts
- PR: https://github.com/CubieCode/wardlock/pull/13
- Depends on: F0.1
- Scope: Finish `.goreleaser.yaml` (server and CLI for linux/darwin/windows on amd64/arm64, checksums, SBOMs, multi-arch image for `ghcr.io/cubiecode/wardlock`), an install script, the systemd unit and compose example, and the Go module tag scheme for vanity paths (see DEPLOYMENT). Publishing stays disabled until a release is explicitly authorised.
- Acceptance: Snapshot release builds every artefact in CI; the image runs as non-root and passes `/healthz`; the install script verifies checksums; a test tag proves the module tag scheme with `go install`; nothing is published.
- Skills: wardlock-item
- Parallel: Wave 1 with F0.2, F0.3 and F0.4. Owns `.goreleaser.yaml`, `deploy/`, `.github/workflows/release.yml`, install script.

### O4.2: Health, metrics, logs and backups

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.2, C1.4
- Scope: Readiness endpoint (database, blob directory, certificates); Prometheus-format metrics on an operator-only listener; structured log fields reviewed; `wardlockd backup` using SQLite's online backup API; restore documentation and test.
- Acceptance: A test backs up a live database during writes and restores it into a working instance; metrics are not reachable on site or dashboard hosts; DEPLOYMENT updated.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 5 or later with any lane not touching `internal/platform` or `cmd/wardlockd`.

### O4.3: Documentation site on Wardlock

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.5, A2.2
- Scope: A static docs site generated from `docs/` (user-facing pages only), deployed with `wardlock deploy` to a Wardlock instance; a CI job that builds it (deploying only when authorised).
- Acceptance: The site builds in CI with no external services; links are checked; it is protected or public as decided at assignment; no deploy happens without explicit authorisation.
- Skills: wardlock-item
- Parallel: Any wave after its dependencies; owns `docs-site/` (new) and its workflow.

### O4.4: Public alpha launch checklist

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.7, O4.1, O4.2
- Scope: A checklist document only: licence and CLA readiness, security review closed, release artefacts verified, vanity import tags served from `wardlock.nz`, repository visibility, private vulnerability reporting enabled, CLA Assistant enabled, docs reviewed. No launch actions.
- Acceptance: Checklist merged with an owner for every line; nothing published, no settings or DNS changed.
- Skills: wardlock-item
- Parallel: Alone, last.

## Waves

Suggested batches, based on the file ownership above. Re-check overlap before launching; merge in the order listed within a wave when items share interfaces.

1. **Wave 1:** F0.2, F0.3, F0.4, O4.1. F0.3 is the migration and lockfile owner; F0.2 owns the API contract; F0.4 owns `internal/mail`; O4.1 owns `deploy/` and release config.
2. **Wave 2** (after F0.2 and F0.3): C1.1, C1.2, C1.3, C1.4. C1.4 is the lockfile owner; no migrations expected. Merge C1.2 before C1.3 (C1.3 consumes `BlobStore` and `Notifier`).
3. **Wave 3:** C1.6, C1.7, A2.1, A2.8, with D3.1 as soon as a worker frees up (it is the only UI lane). C1.6 is the lockfile owner and, if needed, the migration owner. A2.1 owns the access cookie gate.
4. **Wave 4:** C1.5, A2.2, D3.2, C1.8. API additions for A2.2 agreed before the wave; C1.5 merges before A2.2's CLI commands.
5. **Wave 5:** A2.3, A2.4, A2.6, C1.9 (O4.2 after C1.9). A2.3 lands the shared login page template first.
6. **Wave 6:** A2.5, D3.3, D3.4, D3.5. Only one of D3.3, D3.4 and D3.5 changes `packages/ui`.
7. **Then:** A2.7 alone; O4.3 when its dependencies are done; O4.4 last.

## Not planned yet

These are recorded so nobody starts them. Each needs a roadmap item and an explicit assignment first.

- MCP server for creating and managing links from AI tools.
- Teams and members: shared ownership of sites, roles.
- Scale-out: Postgres repositories passing the same contract tests, an S3 `BlobStore`, shared certmagic storage, a shared rate limiter, multi-instance cache invalidation.
- Hosted service concerns: billing, quotas, abuse reporting and scanning.
- Homebrew tap and other package managers.

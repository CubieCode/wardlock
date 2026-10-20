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

- Status: `done`
- Owner: `feat/c1.5-cli-deploy-commands`
- PR: https://github.com/CubieCode/wardlock/pull/41
- Depends on: C1.2, C1.7
- Scope: `wardlock deploy`, `list`, `rollback`, `teardown`; `wardlock.json` and `.wardlockignore` (gitignore syntax, documented defaults); parallel bounded uploads with progress output; `--json` output for every command, no prompts without a TTY; `WARDLOCK_TOKEN` for CI; exit codes per SPEC.
- Acceptance: Tests against an in-process `wardlockd` (or the generated client's test server) cover a first deploy, a no-change deploy, ignore rules, every `wardlock.json` field and unknown-field rejection, rollback, teardown confirmation, `--json` schemas (documented), and network failure messages; `--help` lists exactly the built commands; works on Linux, macOS and Windows path separators (unit tests).
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 4 with A2.2, D3.2 and C1.8. Owns `apps/cli/`. No server changes.

### C1.6: Owner passkeys

- Status: `done`
- Owner: `feat/c1.6-owner-passkeys`
- PR: https://github.com/CubieCode/wardlock/pull/34
- Depends on: C1.1
- Scope: WebAuthn passkey registration and sign-in for owners (relying party = dashboard host), listing and removing passkeys; dashboard sign-in screen offers passkeys alongside email code once D3.1 exists.
- Acceptance: Ceremony tests with a software authenticator; replayed or cross-origin assertions rejected; two-owner isolation for passkey management; browser test with a virtual authenticator once D3.1 is done.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.7, A2.1 and A2.8. Owns passkey code in `internal/owners/`; lockfile owner for wave 3 (WebAuthn library). May need a migration if F0.3 omitted passkey tables: then it is the wave's migration owner.

### C1.7: API tokens and CLI browser login

- Status: `done`
- Owner: `feat/c1.7-api-tokens-cli-login`
- PR: https://github.com/CubieCode/wardlock/pull/35
- Depends on: C1.1
- Scope: Scoped, named, revocable API tokens (`deploy`, `manage`) in the shared token format; `Bearer` authentication for the API; device-code CLI login (`wardlock login`, `logout`), approval page on the dashboard host; token storage in the OS keychain with a 0600 file fallback; `WARDLOCK_TOKEN` override.
- Acceptance: Tests cover token hashing and constant-time compare, scope enforcement on every C1 operation, revocation, expired login requests, polling rate limits, keychain fallback file permissions; tokens never logged (asserted); `wardlock login`/`logout` appear in `--help`.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.6, A2.1 and A2.8. Owns token code in `internal/owners/` (coordinate file split with C1.6 in the briefs) and `apps/cli` login files. Adds the keychain library only via the wave's lockfile owner.

### C1.8: Blob garbage collection

- Status: `done`
- Owner: `feat/c1.8-blob-gc`
- PR: https://github.com/CubieCode/wardlock/pull/33
- Depends on: C1.2
- Scope: Periodic and on-demand (`wardlockd gc`) removal of blobs referenced by no deploy, with a grace period covering in-flight uploads; pruning deploy history beyond the configured limit (never the active deploy); metrics of bytes freed in logs.
- Acceptance: Tests prove referenced blobs and blobs younger than the grace period survive, concurrent upload plus GC never loses a blob needed by activation, and history pruning keeps the active deploy.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 4 with C1.5, A2.2 and D3.2. Owns GC files in `internal/deploys/`.

### C1.9: Custom domains

- Status: `done`
- Owner: feat/c1.9-custom-domains
- PR: https://github.com/CubieCode/wardlock/pull/51
- Depends on: C1.3, C1.4, C1.5
- Scope: Custom domains per SPEC "Custom domains": hostname normalisation and rejection rules in `internal/sites`; add, list, get, verify and remove behind the F0.2 operations; a `Resolver` interface (CNAME chain, TXT) with a `net.Resolver` implementation honouring `WARDLOCK_DNS_RESOLVERS` and an in-test fake; a verification worker (initial backoff, 24-hour re-checks, 72-hour lapse, automatic recovery) started from `cmd/wardlockd`; `Notifier` events so the edge and the on-demand TLS decision see status changes immediately; certificate removal on detach; `/.wardlock/tls-ask` for behind-a-proxy mode; `wardlock domain add|list|verify|remove`; the on-demand block in `deploy/Caddyfile.example`; DEPLOYMENT "Custom domains". Implement the `Domains` repository methods.
- Acceptance: Table-driven tests over the fake resolver cover CNAME direct and chained, CNAME to another site or elsewhere, TXT fallback (apex, proxied), wrong and stale tokens, DNS errors and timeouts, backoff timing, lapse after 72 hours and recovery, all with an injected clock; normalisation tests (case, trailing dot, IDN to A-label, IPs, wildcards, base domain, dashboard and auth hosts); `domain_taken` reveals no site or owner; two-owner isolation (owner B cannot list, verify, remove or see owner A's domains: same 404); a re-claim after removal needs a new token; end-to-end `httptest` proving pending and lapsed hosts are not served and the on-demand decision denies them; `tls-ask` answers only trusted-proxy sources in proxy mode; CLI tests including `--json` and `--wait`; contract tests for every new repository method; verification tokens never logged.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.3, A2.4 and A2.6. Owns domain files in `internal/sites/`, `queries/domains.sql`, the domain worker wiring and `WARDLOCK_DNS_RESOLVERS` config lines, `apps/cli` domain commands, `deploy/Caddyfile.example`. Touches `internal/edge` only for `tls-ask` (coordinate with A2.3's login template work in the briefs). Conflicts with O4.2 on `internal/platform` and `cmd/wardlockd`: run O4.2 after it. No migrations or API changes if F0.2 and F0.3 covered domains as scoped; otherwise it is the wave's migration owner and needs the API gate.

### C1.10: Long-term caching for hashed assets

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.3, C1.5, A2.12
- Scope: The per-deploy opt-in SPEC "Serving" lists as not in v1. A deploy may set `immutableAssets` (like `spa`, stored on the deploy so a rollback restores matching headers): `wardlock deploy --immutable-assets` and `"immutableAssets": true` in `wardlock.json`, a deploy field and create parameter in `openapi.yaml`, a portable migration adding the deploy column, the sqlc query and `storetest` contract cases. When the active deploy sets it, files under `/assets/` whose name carries a content hash (define the rule in SPEC, for example a dot- or dash-separated segment of 8 or more hex or base64url characters before the extension) are served with `Cache-Control: public, max-age=31536000, immutable`, or `private, max-age=31536000, immutable` on protected sites; everything else keeps the current headers. Show the flag on deploys in the dashboard (no `packages/ui` change). Update SPEC, ARCHITECTURE (schema diagram, migration list), DEPLOYMENT if relevant, and the CLI help.
- Acceptance: Edge tests for the header rule (hashed and unhashed names, outside `/assets/`, protected and public, renditions, flag off); migration and contract tests; CLI tests for the flag and `wardlock.json`; API tests; dashboard test; `pnpm codegen` output committed; `pnpm check` and `pnpm browser:test` pass; the dashboard change checked at 1440px and 390px in light and dark.
- Skills: wardlock-item, wardlock-go-service, wardlock-access, wardlock-web
- Parallel: Wave 12, second group, after A2.12 merges. Migration owner for the wave; takes the API contract gate from A2.12.

### C1.11: S3-compatible blob storage

- Status: `review`
- Owner: feat/c1.11-s3-blobstore
- PR: https://github.com/CubieCode/wardlock/pull/91
- Depends on: C1.2, C1.8
- Scope: A second `deploys.BlobStore` implementation backed by any S3-compatible object store (AWS S3, Cloudflare R2, MinIO, Backblaze B2, Wasabi, Garage), chosen by configuration; the filesystem store stays the default. `Has` via `HeadObject`; `Put` streams while hashing to a temporary key, verifies SHA-256 and size, then copies to the final key (a mismatch stores nothing); `Open` returns a seekable reader that fetches ranges on demand, so the edge's Range and content sniffing work without downloading whole files; `Delete` removes the object. Garbage collection gets an S3 equivalent of the filesystem-specific sweeps (stale temporary keys). Configuration such as `WARDLOCK_BLOB_BACKEND=s3`, `WARDLOCK_S3_ENDPOINT`, `_BUCKET`, `_REGION`, `_PREFIX` and path-style addressing, with credentials from the standard AWS environment and files; credentials never logged; readiness checks the bucket. Docs: DEPLOYMENT (setup, bucket permissions, request costs, backups by bucket versioning or replication since `wardlockd backup` does not copy the bucket), ARCHITECTURE, `.env.example`. Still single-instance: the database, limiters and certificates stay local.
- Acceptance: The `BlobStore` contract tests run against both implementations, the S3 one against an in-process fake (no Docker or network in `pnpm check`); tests for hash mismatch, ranged reads and seeks, missing blobs, GC of stale temporary keys and config validation; the new dependency justified in the PR and `go.sum` updated; `pnpm check` and `git diff --check` pass.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 12, beside C1.10. Lockfile owner for `apps/server/go.sum`; owns `internal/deploys` blob stores, `cmd/wardlockd` wiring and `internal/platform` config. No migration or API contract change.

### C1.12: Fewer object-store calls

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.10, C1.11, O4.12, O4.14
- Scope: Cut the object-store (R2/S3) requests per page view and per upload, without weakening integrity. (1) Lazy open: the edge already knows each file's size from the deploy manifest, so opening a blob for serving makes no request (no `HeadObject`); a missing object surfaces on the first read and is handled as today's open failure. Change the `BlobStore` interface minimally (for example an open that takes the known size) and keep the filesystem store's behaviour. (2) Early `304`: answer `If-None-Match` (and `If-Modified-Since` where `http.ServeContent` would) from the known ETag (the SHA-256) before opening the blob, for both stores, with identical headers to today's `304`. (3) Single-call upload: when the store proves the provider verifies the payload hash (an explicit SHA-256 checksum or signed payload hash that the provider rejects on mismatch), `Put` writes straight to the final key in one request; otherwise it keeps today's temporary key, verify and copy. Detect support with a one-time probe per process (an upload with a deliberately wrong checksum to a temporary key that must be rejected), never by provider name, and never let an unverified write reach a final key. (4) A local read-through blob cache: `WARDLOCK_BLOB_CACHE_DIR` and `WARDLOCK_BLOB_CACHE_MAX_BYTES` (off by default), content-addressed so it can never be stale, filled on a miss by streaming to a temporary file while serving and verifying the hash before keeping it, evicting least recently used blobs beyond the size limit, safe under concurrent requests for the same blob, rebuilt from scratch if wiped, and removed from when GC deletes a blob. (5) Recommend daily GC (`WARDLOCK_GC_INTERVAL_MINUTES=1440`) and a cache size in `deploy/vps/wardlock.env.example`, and document the cache, the probe and the call counts per request in DEPLOYMENT and ARCHITECTURE.
- Acceptance: Tests with the in-process S3 fake counting requests: a full response costs one `GetObject` on a cold cache and none on a warm one; `304` and `HEAD` cost none; a Range request on a cold cache costs one ranged `GetObject`; single-call upload used only when the fake enforces checksums, and the temporary-key path when it does not, with a mismatch never leaving an object at the final key in either; cache tests for hash verification, eviction, concurrent fills, wipe and GC removal; edge tests that conditional responses are unchanged; `pnpm check` passes.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 12, after C1.10 merges (both edit `internal/edge/files.go`). Owns `internal/deploys` blob stores and cache, the edge's blob open path, `internal/platform` cache config and `deploy/vps/wardlock.env.example`; no migration, API or lockfile change expected.

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

- Status: `done`
- Owner: `feat/a2.2-share-links`
- PR: https://github.com/CubieCode/wardlock/pull/43
- Depends on: A2.1, A2.8, C1.7
- Scope: Everything in [wardlock-access](.agents/skills/wardlock-access/SKILL.md) for links: create with label, expiry or never, use limit and path scope; `?t=` and `Authorization: Bearer`; 200 on first request with `Set-Cookie`; HTML-only `history.replaceState` strip; check on every request; revoke and rotate; use-limited interstitial counting uses on POST; access log entries per access; API operations and `wardlock share create|list|revoke|rotate`.
- Acceptance: The full wardlock-access test matrix passes; fuzz test for token parsing; `curl` and a no-JS, no-cookie client get the content on the first request; links never appear raw in logs or the access log (asserted).
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 4 with C1.5, D3.2 and C1.8. Owns link files in `internal/access/`, link queries and `apps/cli` share commands (coordinate `apps/cli` files with C1.5: separate files, C1.5 merges first). API contract additions are a gate: agree them before the wave.

### A2.3: Password policy

- Status: `done`
- Owner: feat/a2.3-password-policy
- PR: https://github.com/CubieCode/wardlock/pull/46
- Depends on: A2.1
- Scope: Per-policy password (Argon2id), `/.wardlock/login` password form, attempt rate limiting through `Limiter` (in-memory implementation if not yet present), password change ending password sessions, `wardlock policy set --password` reading from prompt or stdin.
- Acceptance: Tests cover correct and wrong passwords, rate limits and reset, session invalidation on change, timing-safe comparison, login page `no-store` and no password in logs.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.4 and A2.6. Owns password files in `internal/access/` and the login page template. Login page shell shared with A2.4: A2.3 lands the shared template first or both agree it in their briefs. Lockfile owner if Argon2 needs `golang.org/x/crypto`.

### A2.4: Email-code policy

- Status: `done`
- Owner: feat/a2.4-email-code-policy
- PR: https://github.com/CubieCode/wardlock/pull/52
- Depends on: A2.1, F0.4
- Scope: Email-code method with address and domain allowlists, neutral responses, 6-digit codes, send and verify rate limits, use of the F0.4 mailer and dev sink.
- Acceptance: Tests cover allowlist matching (exact, domain, `*.domain`), neutral responses for disallowed addresses, expiry, attempts, rate limits per IP, address and site, and codes never logged outside the dev sink.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.3 and A2.6. Owns email-code files in `internal/access/`. Shares the login page with A2.3 (see A2.3).

### A2.5: OAuth and OIDC

- Status: `done`
- Owner: `feat/a2.5-oauth-oidc`
- PR: https://github.com/CubieCode/wardlock/pull/55
- Depends on: A2.1, C1.4
- Scope: Central auth origin `auth.<base domain>`; GitHub, Google and generic OIDC providers configured by the operator; PKCE, `state`, OIDC `nonce`; single-use, 60-second, site-bound codes exchanged at `/.wardlock/callback`; allowlists by verified email, domain and GitHub organisation.
- Acceptance: Tests against in-process fake providers cover the full flow, replayed and expired codes, wrong-site codes, `state` and PKCE failures, unverified emails and each allowlist type; no cookie is ever set on the base domain.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 6 with D3.3 and D3.4. Owns OAuth files in `internal/access/` and the auth-origin route in `internal/edge/`. Lockfile owner if an OIDC library is added.

### A2.6: AI-readable renditions

- Status: `done`
- Owner: feat/a2.6-ai-renditions
- PR: https://github.com/CubieCode/wardlock/pull/42
- Depends on: A2.1, C1.2
- Scope: HTML to Markdown and plain text at deploy time, stored as blobs referenced from the manifest; `?format=md|txt` with correct content types; `Link: rel="alternate"` on HTML; documented conversion contract in SPEC; golden-file fixtures.
- Acceptance: Golden tests over `fixtures/sites/` (headings, lists, tables, code, links without tokens, images' alt text, scripts and styles removed); `?t=…&format=md` works with no cookies and no JavaScript; protected renditions are denied without access; renditions never leak `t` into links.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 5 with A2.3 and A2.4. Owns `internal/deploys/renditions*` and the format selection in `internal/edge/`. Lockfile owner if an HTML parser beyond `golang.org/x/net/html` is needed.

### A2.7: Access security review

- Status: `done`
- Owner: `feat/a2.7-access-security-review`
- PR: https://github.com/CubieCode/wardlock/pull/65
- Depends on: A2.2, A2.3, A2.4, A2.5, A2.6, D3.3
- Scope: Walk docs/SECURITY-MODEL.md against the implementation; fuzz token parsing and cookie decoding; verify every rate limit; check headers on every protected response type; check log hygiene; fix small findings, record larger ones as new roadmap items.
- Acceptance: A findings table in the PR with each threat's status and evidence; fuzzers committed and run for a recorded duration; no open high-severity finding without an item.
- Skills: wardlock-item, wardlock-access, wardlock-go-service
- Parallel: Runs alone (reads everything; may touch any access file).

### A2.8: Access log pipeline

- Status: `done`
- Owner: `feat/a2.8-access-log`
- PR: https://github.com/CubieCode/wardlock/pull/24
- Depends on: F0.3
- Scope: Buffered, batched access-log writer off the request path (flush every second or 500 entries, drop-and-count when full, flush on shutdown); configurable IP truncation; retention pruning; `AccessLog` repository methods; query by site and link.
- Acceptance: Tests prove the request path never blocks on the database (a slow fake store does not slow serving), counts dropped entries, flushes on shutdown, truncates IPv4 and IPv6 as configured and never stores raw tokens.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 3 with C1.6, C1.7 and A2.1. Owns `internal/access/accesslog*` (or a dedicated package agreed in the brief) and access-log queries.

### A2.9: Sign-in limiter tables that resist flooding

- Status: `done`
- Owner: `feat/a2.9-limiter-flooding`
- PR: https://github.com/CubieCode/wardlock/pull/72
- Depends on: A2.7
- Scope: A2.7 found that the visitor per-IP limiter table (`internal/access`, 100,000 keys, IPv6 by /64) is shared by password attempts, email-code sends and entries, OAuth starts and share-link guesses, each counted before any site or credential check: filling it (about two IPv6 /48s) refuses password, email-code and OAuth sign-in for every new client on every site until its windows expire. Separately, anyone can spend a site's per-site password and email-code limits and keep that method shut for the site indefinitely. Likewise, the owner per-address send table (`internal/owners`, 100,000 addresses, kept apart from the owner per-IP table by A2.7) can be filled with invented addresses, which refuses owner email-code sends to addresses it does not yet hold for up to an hour. Decide and implement a design that keeps sign-in available under both floods without lifting any limit (for example per-method tables, coarser keys such as IPv6 /48 under pressure, counting only failures per site, or per-site fallbacks when the IP table is full), and record the decision in docs/SECURITY-MODEL.md, replacing the "Accepted limiter risks" paragraph.
- Acceptance: Tests prove that filling the IP table from many synthetic addresses neither refuses sign-in for unseen clients beyond what the recorded design allows nor lifts any per-IP, per-address or per-site limit; that filling the owner per-address table does not refuse owner email-code sign-in beyond the recorded bound; that a per-site flood cannot hold a method shut beyond the recorded bound; and that the full wardlock-access test matrix passes. SECURITY-MODEL and SPEC describe the new behaviour.
- Skills: wardlock-item, wardlock-access, wardlock-go-service
- Parallel: Runs alone or beside items that do not touch `internal/access/limit*`, the access service's limiter wiring or `internal/edge` rate-limit handling.

### A2.10: Third-party log hygiene

- Status: `done`
- Owner: `feat/a2.10-third-party-log-hygiene`
- PR: https://github.com/CubieCode/wardlock/pull/71
- Depends on: A2.7
- Scope: A2.7 checked Wardlock's own slog calls; two log paths it did not verify end to end remain. The certmagic log adapter (`internal/tls/log.go`) forwards every field verbatim, so at debug level ACME and DNS-provider details, including provider responses, reach the logs unfiltered. The `dev://` mail sink logs visitor recipient addresses, which is intended for local development but is the default mail mode. Review both: redact or drop sensitive certmagic fields, and warn at startup when `dev://` mail is used outside local mode, or record why not.
- Acceptance: Tests capture certmagic debug output through the adapter and assert no credential-bearing field survives; a test or documented decision covers the `dev://` warning; SECURITY-MODEL lists what each third-party adapter may log.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Any wave; touches only `internal/tls/log*`, `internal/mail/dev*` and `cmd/wardlockd` startup checks.

### A2.11: Distinct error code for the domain-check cooldown

- Status: `done`
- Owner: `feat/a2.11-domain-cooldown-code`
- PR: https://github.com/CubieCode/wardlock/pull/70
- Depends on: A2.7
- Scope: A2.7 found that the domain-check cooldown and a refused API token both answer `429` with code `rate_limited` (`internal/api/domains.go` and the token middleware in `internal/api/owners.go`), so `wardlock domain add --wait` treats a refused-token 429 as a cooldown and keeps polling until its 10-minute deadline without naming the token, and `wardlock domain verify` reports "checked moments ago". Give the cooldown 429 its own error code in `openapi.yaml`, send it from `internal/api/domains.go`, and make `domain add --wait` and `domain verify` retry or report a cooldown only for that code and show the API's error for any other 429.
- Acceptance: API tests prove the cooldown answers the new code and a refused token answers `rate_limited`; CLI tests prove `domain add --wait` stops at once with the server's message on a `rate_limited` 429 and still waits out a cooldown, and `domain verify` reports each case correctly; regenerated clients committed; SPEC describes the code.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Holds the `packages/api-spec` gate; touches `internal/api/domains*`, `apps/cli/cmd/wardlock/domain*` and the generated clients.

### A2.12: Per-site access log

- Status: `review`
- Owner: `feat/a2.12-site-access-log`
- PR: https://github.com/CubieCode/wardlock/pull/88
- Depends on: A2.8, D3.3
- Scope: Let owners read a whole site's access log, not just one share link's (O4.6 found SPEC promised it). Add `listSiteAccessLog` (`GET /api/v0/sites/{site}/access-log`) to `packages/api-spec/openapi.yaml`, paged and filtered like `listShareLinkAccessLog` and readable with either API token scope, served from the existing owner-scoped `store.AccessLog.ListBySite`; a missing site and another owner's answer the same `404`, and entries of a torn-down site are not reachable through it. Add an "Access log" page to the dashboard, reached from the site page, showing time, path, status, how the visitor got in (with the share link's label when it came through one) and truncated IP, with the same empty, loading, error and paging states as the link access log. Update SPEC "Access log" and the API docs. No CLI command, matching the link log.
- Acceptance: API handler tests (paging, scope, owner isolation with two owners, missing site `404`); dashboard tests and a Playwright spec; Mobbin references cited; checked at 1440px and 390px in light and dark; `pnpm check` and `pnpm browser:test` pass.
- Skills: wardlock-item, wardlock-go-service, wardlock-access, wardlock-web
- Parallel: Wave 12, first group. Holds the API contract gate (`openapi.yaml` and generated clients) and the `packages/ui` gate if it needs a component. Owns `internal/api` access-log handlers and a new `apps/web/src/sites/` access-log page; adds one link to `site-page.tsx` (merge after D3.6 and rebase).

### A2.13: Operator controls and abuse reports

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.12, C1.12
- Scope: Give the operator the controls needed to act on abuse, as `wardlockd admin` commands (no owner-facing API or dashboard admin area). Suspend and reinstate an owner: a suspended owner's dashboard sessions, API tokens and CLI logins stop working at once, sign-in is refused with a neutral message, and their sites stop serving. Take down and restore a site: a taken-down site answers a neutral, `no-store` "This site is unavailable" page on every host (including custom domains) without touching its deploys or files, which are kept as evidence until the operator tears the site down. Restrict invitations: `WARDLOCK_INVITES=owners` (today's behaviour, the default) or `operator`, where only `wardlockd admin` can invite. Abuse reports: a "Report this site" link on the `/.wardlock/` pages and the unavailable page leads to a small form (site, reason, optional contact) that emails `WARDLOCK_ABUSE_EMAIL` and is rate-limited like the other visitor forms; reports never include share-link tokens. Every operator action is logged with IDs only. A portable migration adds the suspension and takedown state. Document it all in SPEC, SECURITY-MODEL "Public instances and abuse" and DEPLOYMENT, including the preserve-and-report procedure for illegal material.
- Acceptance: Tests: suspension ends every credential kind and stops serving; takedown serves the unavailable page on subdomain and custom-domain hosts and keeps files; restore brings both back; invitations refused for owners under `WARDLOCK_INVITES=operator`; the report form's rate limit and no-token rule; migration and contract tests; the wardlock-access matrix rows that apply; `pnpm check` and `pnpm browser:test` pass; the report form and unavailable page checked at 1440px and 390px in light and dark with Mobbin references.
- Skills: wardlock-item, wardlock-go-service, wardlock-access, wardlock-web
- Parallel: Wave 13, first. Migration owner.

### A2.14: Owner quotas, video limits and blocked file types

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.13
- Scope: Instance-wide limits with per-owner overrides set by `wardlockd admin`: total stored bytes per owner (counting each blob once per owner), number of sites per owner, and deploys per owner per day. A separate per-file limit for video files (by extension and declared type: `.mp4`, `.webm`, `.mov`, `.m4v`, `.mkv`, `.avi`, and the like), `WARDLOCK_MAX_VIDEO_SIZE`, defaulting well below the general per-file limit (propose a default in the PR), with `0` refusing video entirely. An optional blocklist of file extensions refused at manifest time, `WARDLOCK_BLOCKED_EXTENSIONS`, with a documented suggested list of executables and installers (off by default). Every refusal is a `413 limit_exceeded` or `422` naming the file and the limit, shown clearly by the CLI and dashboard. A portable migration for the per-owner overrides. Update SPEC, DEPLOYMENT, `deploy/vps/wardlock.env.example` and the API contract's error descriptions.
- Acceptance: Tests for each limit at, under and over the boundary, per-owner overrides, video detection by extension, the blocklist, error bodies naming the file; CLI and dashboard tests for the messages; migration and contract tests; `pnpm check` and `pnpm browser:test` pass; any dashboard change checked at 1440px and 390px in light and dark.
- Skills: wardlock-item, wardlock-go-service, wardlock-web
- Parallel: Wave 13, after A2.13 merges. Migration owner and API contract gate holder after A2.13.

### A2.15: Content screening with OpenAI moderation

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.13, A2.14
- Scope: Optional automated screening of uploaded content, off unless the operator sets `WARDLOCK_MODERATION=openai` and `WARDLOCK_OPENAI_API_KEY` (a secret, never logged). A `Moderator` interface in its own package with an OpenAI implementation over the moderation endpoint (`omni-moderation-latest`, text and images), and a fake for tests. After a deploy is activated, each image and HTML file not yet screened (by content hash, so each unique file is checked once ever) is queued in the background; HTML is checked as its extracted text, images in the formats the endpoint accepts, within its size limits; other types and videos are not sent (videos are bounded by A2.14). Results are stored per hash: allowed, flagged (operator review) or quarantined (categories the operator configures to quarantine, defaulting to sexual content involving minors, plus sexual content where the operator chooses). A quarantined file is not served from any site (a neutral placeholder or `451`), the deploy keeps working otherwise, the operator is emailed and the owner told which file was withheld. `wardlockd admin` lists flagged and quarantined files with their sites, and releases or confirms them. Per-owner exemption: `wardlockd admin moderation disable --owner <email>` (and `enable`), so the operator can turn screening off for trusted owners; new owners follow the instance default. Failures and rate limits back off and retry; screening never blocks deploys or serving. Content leaves the server only when enabled: document this in SPEC, SECURITY-MODEL and DEPLOYMENT, and in the acceptable use policy. A portable migration for results and the exemption flag.
- Acceptance: Tests with the fake moderator: queueing once per hash, each outcome, quarantine stops serving on every host, exempt owners are never sent, retries and back-off, no key or content in logs; an OpenAI client test against `httptest`; migration and contract tests; the wardlock-access matrix rows that apply; `pnpm check` passes.
- Skills: wardlock-item, wardlock-go-service, wardlock-access
- Parallel: Wave 13, after A2.14 merges. Migration owner after A2.14. No lockfile change expected (plain HTTP client).

## D3: Dashboard

### D3.1: Dashboard shell and owner sign-in

- Status: `done`
- Owner: `feat/d3.1-dashboard-shell`
- PR: https://github.com/CubieCode/wardlock/pull/25
- Depends on: C1.1
- Scope: Router, authenticated shell with navigation for built screens only, email-code sign-in and sign-out screens, setup-URL first-owner flow, session expiry handling, design-system additions they need (text field, form errors).
- Acceptance: Browser tests at 1440px and 390px in light and dark cover sign-in, wrong code, expiry and sign-out; component tests for new `packages/ui` components; no navigation to unbuilt screens. Mobbin references cited in the PR (start with: email one-time-code sign-in flow; web app shell with sidebar navigation on desktop and mobile).
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 3 or later, alone among UI lanes (owns `packages/ui` gate and `apps/web/src` shell).

### D3.2: Sites and deploys

- Status: `done`
- Owner: `feat/d3.2-sites-and-deploys`
- PR: https://github.com/CubieCode/wardlock/pull/39
- Depends on: D3.1, C1.2
- Scope: Site list with policy and last deploy, site detail with deploy history, rollback and teardown with clear confirmations, copyable site URL.
- Acceptance: Browser tests for list, empty state, rollback and teardown at both widths and themes; foreign site IDs show the same not-found state. Mobbin references cited in the PR (start with: hosting dashboard project list; deployment history with rollback; delete-project confirmation).
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 4 with C1.5, A2.2 and C1.8. Owns `apps/web/src/sites/`. `packages/ui` additions agreed with no other UI lane in the wave.

### D3.3: Share links

- Status: `done`
- Owner: `feat/d3.3-share-links`
- PR: https://github.com/CubieCode/wardlock/pull/49
- Depends on: D3.2, A2.2
- Scope: Create a link (label, expiry or never, use limit, path scope), show the URL once with a copy action, list links, revoke, rotate, per-link access log.
- Acceptance: Browser tests cover creation, the one-time display, copy, revoke and rotate confirmations and the access log view; tokens never appear after creation, in page titles or in screenshots. Mobbin references cited in the PR (start with: create share link dialog with expiry and permissions; API key shown once with copy; activity or access log table).
- Skills: wardlock-item, wardlock-web, wardlock-access
- Parallel: Wave 6 with A2.5 and D3.4 (D3.4 owns separate screens; only one may change `packages/ui`).

### D3.4: Policies editor

- Status: `done`
- Owner: feat/d3.4-policies-editor
- PR: https://github.com/CubieCode/wardlock/pull/61
- Depends on: D3.2, A2.3, A2.4
- Scope: View and edit the site default and path policies, choose methods, set a password, manage email allowlists, and OAuth allowlists once A2.5 is done.
- Acceptance: Browser tests for each method and for path policy precedence explained in the UI; passwords never echoed back. Mobbin references cited in the PR (start with: page visibility and access settings; password-protect a page; allowlist of emails and domains).
- Skills: wardlock-item, wardlock-web, wardlock-access
- Parallel: Wave 6 with A2.5 and D3.3. Owns `apps/web/src/policies/`.

### D3.5: Custom domains

- Status: `done`
- Owner: feat/d3.5-custom-domains
- PR: https://github.com/CubieCode/wardlock/pull/60
- Depends on: D3.2, C1.9
- Scope: A Domains section on the site detail screen: add a hostname, show the routing CNAME (and the TXT fallback with when to use it) with copy actions, live status with last check and the failure reason in plain words, "Check now", remove with a confirmation that reminds the owner to delete the DNS record.
- Acceptance: Browser tests at 1440px and 390px in light and dark cover add, each status, a failure reason, "Check now", `domain_taken` and removal; foreign site IDs show the same not-found state; records are copyable exactly as shown. Mobbin references cited in the PR (start with: add custom domain with DNS records to configure; domain verification status pending and verified; copy DNS record value).
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 6 with A2.5, D3.3 and D3.4. Owns `apps/web/src/domains/` and the Domains section's mount point in `apps/web/src/sites/`. Only one of D3.3, D3.4 and D3.5 changes `packages/ui`.

### D3.6: Open site from the dashboard

- Status: `review`
- Owner: `feat/d3.6-open-site`
- PR: https://github.com/CubieCode/wardlock/pull/87
- Depends on: D3.3
- Scope: Restore SPEC's "Open site" action. On the site page, an "Open site" button opens the site in a new tab. For a public site (its `/` policy is public) it opens the site's address directly. Otherwise it first creates a share link through the existing `createShareLink` operation, labelled "Opened from dashboard", expiring in 10 minutes, no use limit, scoped to the whole site, and opens its URL; the link appears in the link list and access log like any other and can be revoked. Open the new tab synchronously from the click (then set its location) so pop-up blockers allow it, with `noopener`; show an error if creation fails. Update SPEC "Owners and sign-in" to describe exactly this. No API, `packages/ui` or server changes.
- Acceptance: Component tests (public site opens directly; protected site creates a 10-minute labelled link and opens it; failure shows an error); a Playwright spec opening a protected fixture site; Mobbin references cited; checked at 1440px and 390px in light and dark; `pnpm check` and `pnpm browser:test` pass.
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 12, first group. Owns the Open site button in `apps/web/src/sites/site-page.tsx` and its tests; no gates. Merges before A2.12.

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

- Status: `done`
- Owner: feat/o4.2-health-metrics-backups
- PR: https://github.com/CubieCode/wardlock/pull/62
- Depends on: C1.2, C1.4
- Scope: Readiness endpoint (database, blob directory, certificates); Prometheus-format metrics on an operator-only listener; structured log fields reviewed; `wardlockd backup` using SQLite's online backup API; restore documentation and test.
- Acceptance: A test backs up a live database during writes and restores it into a working instance; metrics are not reachable on site or dashboard hosts; DEPLOYMENT updated.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Wave 5 or later with any lane not touching `internal/platform` or `cmd/wardlockd`.

### O4.3: Documentation site on Wardlock

- Status: `done`
- Owner: `feat/o4.3-docs-site`
- PR: https://github.com/CubieCode/wardlock/pull/58
- Depends on: C1.5, A2.2
- Scope: A static docs site generated from `docs/` (user-facing pages only), deployed with `wardlock deploy` to a Wardlock instance; a CI job that builds it (deploying only when authorised).
- Acceptance: The site builds in CI with no external services; links are checked; it is protected or public as decided at assignment; no deploy happens without explicit authorisation.
- Skills: wardlock-item
- Parallel: Any wave after its dependencies; owns `docs-site/` (new) and its workflow.

### O4.4: Public alpha launch checklist

- Status: `done`
- Owner: `feat/o4.4-launch-checklist`
- PR: https://github.com/CubieCode/wardlock/pull/67
- Depends on: A2.7, O4.1, O4.2
- Scope: A checklist document only: licence and CLA readiness, security review closed, release artefacts verified, vanity import tags served from `wardlock.nz`, repository visibility, private vulnerability reporting enabled, CLA Assistant enabled, docs reviewed. No launch actions.
- Acceptance: Checklist merged with an owner for every line; nothing published, no settings or DNS changed.
- Skills: wardlock-item
- Parallel: Alone, last.

### O4.5: Test suite stability

- Status: `done`
- Owner: `feat/o4.5-test-stability`
- PR: https://github.com/CubieCode/wardlock/pull/73
- Depends on: none
- Scope: Fix the intermittent test failures seen in CI during waves 7 and 8, each at its root cause (a wrong wait, query or assumption in the test, or a real race in the code) rather than by retrying or lengthening timeouts: `apps/web/src/sites/sites.test.tsx` "site detail > shows the address, access and deploy history" (found multiple elements with the text `/assets/`); `apps/web/src/links/links.test.tsx` "link access log > treats a missing or foreign link as not found" (document title still the access log's); `apps/web/src/app.test.tsx` "checks the session once when the tab comes back" (expected 2 calls, got 1); `apps/server/internal/mail` TestSMTPHonoursContext (i/o timeout instead of deadline exceeded under CPU load). If a failure turns out to be a product bug, fix it with a test and describe it in the PR.
- Acceptance: Each named test passes 50 consecutive local runs (Vitest `--repeat` or a loop; `go test -race -count=50` for the Go test), including at least one run under CPU load (for example with `GOMAXPROCS=1` or a parallel `stress` job), with the commands and results in the PR; no test retries, skips or longer timeouts added as the fix; `pnpm check` passes.
- Skills: wardlock-item, wardlock-web, wardlock-go-service
- Parallel: Any wave. Touches only the named test files, test helpers under `apps/web/src/test*` and, if a real race is found, the code under test; no gates.

### O4.6: Alpha documentation refresh

- Status: `done`
- Owner: `feat/o4.6-alpha-docs`
- PR: https://github.com/CubieCode/wardlock/pull/76
- Depends on: O4.4, A2.9, A2.10, A2.11
- Scope: Make the documentation true for the public alpha. Replace "pre-alpha" with an alpha notice in README, CONTRIBUTING, docs/DEPLOYMENT.md and the docs site (`docs-site/src/build.mjs`, `docs-site/src/render.mjs` and their test); rewrite README's status paragraph and "What it will do" for features that are built. Correct statements in README, CONTRIBUTING, SECURITY, `docs/`, `docs-site/README.md`, `deploy/`, `fixtures/README.md`, DESIGN.md and the `wardlock-*` skills that contradict main (planned or unbuilt features that are now built, renamed packages, env vars, commands or endpoints, and wave 9 risks described as open). Refresh docs/LAUNCH-CHECKLIST.md: close the A2.9 to A2.11 decisions and rows, and re-run its _done_ checks on the new commit. Statements that stay true until the release is published (no `go-import` metadata on `wardlock.nz`, no published releases) stay. No behaviour, API or code changes beyond the docs-site notice strings.
- Acceptance: No "pre-alpha" remains; every corrected claim is checked against the code with file references in the PR; `pnpm check` (including `pnpm docs:build` and its link check) and `git diff --check` pass; nothing published, no settings or DNS changed.
- Skills: wardlock-item
- Parallel: Wave 10, first. Owns the files above; no gates.

### O4.7: Contributor licence agreement text

- Status: `done`
- Owner: `feat/o4.7-cla-text`
- PR: https://github.com/CubieCode/wardlock/pull/77
- Depends on: O4.6
- Scope: Write the Wardlock Contributor Licence Agreement that CONTRIBUTING describes, as `CLA.md` at the repository root: CubieCode as the recipient, a copyright licence including the right to sublicense and relicense (so CubieCode can keep the AGPL/MIT split coherent and move parts between licences), a patent licence, the contributor keeping copyright, the contributor's representations, and how corporate contributors sign. Link it from CONTRIBUTING and update the launch checklist's CLA text row. The text is a draft for the maintainer's (and ideally a lawyer's) review: no gist is created and CLA Assistant is not enabled.
- Acceptance: `CLA.md` merged and linked from CONTRIBUTING; open legal choices (the registered entity name, governing law, any commitment to keep contributions under an OSI-approved licence) listed in the PR; `pnpm check` and `git diff --check` pass; nothing published, no settings changed.
- Skills: wardlock-item
- Parallel: Wave 10, after O4.6 (both edit CONTRIBUTING and the launch checklist). No gates.

### O4.8: Enforce every module boundary in depguard

- Status: `done`
- Owner: `feat/o4.8-depguard-boundaries`
- PR: https://github.com/CubieCode/wardlock/pull/83
- Depends on: O4.6
- Scope: O4.6 found that `.golangci.yml` enforces only some of docs/MODULES.md's "Must not import" rules: those for `edge`, `access`, `oauth`, `mail` and `tls`, the storage rule for domain packages, and the MIT modules. Add depguard rules for the rest: `platform` imports no other internal package; `store` and `dashboard` import no domain package (and `dashboard` not `store`); `deploys` and `sites` import neither `api` nor `edge`; `owners` and `api` do not import `edge`. Each rule gets a `desc` naming the boundary. Update MODULES so it says every row is enforced. The code already respects these rules, so no Go code changes.
- Acceptance: golangci-lint passes on main's code with the new rules; for each new rule, a temporary violating import (not committed) is shown failing with that rule's message, with the commands and output in the PR; `pnpm check` and `git diff --check` pass.
- Skills: wardlock-item, wardlock-go-service
- Parallel: Any wave. Owns `.golangci.yml` and docs/MODULES.md; no gates.

### O4.9: Stop uploading unused CI artifacts

- Status: `done`
- Owner: `feat/o4.9-snapshot-artifacts`
- PR: https://github.com/CubieCode/wardlock/pull/82
- Depends on: O4.1
- Scope: The `Release` workflow uploaded every snapshot (about 65 MB of archives and SBOMs) as an artifact on every PR and main run with 7-day retention, which filled CubieCode's Actions artifact quota until the included usage resets; every PR's docs-site `build` and Release `snapshot` jobs then failed at the upload step. Nothing reads those uploads, so stop them: remove the snapshot upload (PRs and main still build the snapshot and run `scripts/release-smoke.sh`) and the docs-site upload, and have the disabled docs-site deploy job build the site itself instead of downloading an artifact. Keep CI's Playwright failure upload, but with `continue-on-error` so a full quota cannot fail the job. Update the workflow comments and any docs that describe the uploads.
- Acceptance: PR and main runs upload no snapshot or docs-site artifact and pass while the quota is full; the deploy job (still `if: false`) builds the site before deploying it; `pnpm check` and `git diff --check` pass; nothing published or deployed.
- Skills: wardlock-item
- Parallel: Any wave. Owns `.github/workflows/release.yml`; no gates.

### O4.10: Release notes for v0.1.0

- Status: `todo`
- Owner:
- PR:
- Depends on: O4.6, D3.6, A2.12, C1.10
- Scope: Write the alpha release notes by hand (changelog generation is off), as `docs/releases/v0.1.0.md`: the alpha warning (no external security review, not for anything sensitive), what Wardlock does, how to install (release archives with `install.sh`, the container image, `go install` once the vanity paths are served), the first-run and backup and upgrade pointers into DEPLOYMENT, and how to report problems. Update the launch checklist's "Release notes" row. Nothing tagged or published.
- Acceptance: Notes merged and accurate against main; links valid in `pnpm docs:build`; `pnpm check` and `git diff --check` pass.
- Skills: wardlock-item
- Parallel: Wave 12, last. Owns `docs/releases/`; no gates.

### O4.11: Publish job ready to enable

- Status: `review`
- Owner: `feat/o4.11-publish-job`
- PR: https://github.com/CubieCode/wardlock/pull/86
- Depends on: O4.1, O4.9
- Scope: The launch checklist's "Publish job ready to enable" line. In `.github/workflows/release.yml`, trigger on `v*` tags (the server/CLI `v0.1.0` tag, not the `apps/*/v*` and `packages/*/v*` module tags), add the ghcr.io login (with `packages: write` and `contents: write` scoped to the publish job only), and remove `if: false` so the job runs only on such a tag push with `WARDLOCK_PUBLISH=true`. Snapshots on PRs and main stay as they are and never publish. Keep the GitHub release a draft. Update the workflow header, `docs/DEPLOYMENT.md`'s release steps and the launch checklist row. No tag is pushed.
- Acceptance: The workflow shows publishing can only run from a `v*` tag push; PR and main runs still skip `publish`; `pnpm check` passes; nothing tagged, published or pushed to ghcr.io.
- Skills: wardlock-item
- Parallel: Wave 12, first group. Owns `.github/workflows/release.yml`, `.goreleaser.yaml` if needed; no gates.

### O4.12: VPS deployment with automatic TLS and R2

- Status: `review`
- Owner: `feat/o4.12-vps-deploy`
- PR: https://github.com/CubieCode/wardlock/pull/93
- Depends on: C1.4, C1.9, C1.11, O4.11
- Scope: A ready-to-use deployment for one small VPS with a public IP (any provider; OVHcloud VPS-1 as the worked example), with Wardlock terminating TLS itself and uploads in Cloudflare R2. Add `deploy/vps/`: a `compose.yaml` running only `wardlockd` (the published `ghcr.io/cubiecode/wardlock:<version>` image pinned by version, ports 80 and 443 published, read-only root, dropped capabilities, the operator listener bound to loopback, one data volume for SQLite and certificates), a `wardlock.env.example` (`WARDLOCK_TLS_MODE=auto` with `WARDLOCK_DNS_PROVIDER=cloudflare` for the wildcard, the hosts, `WARDLOCK_BLOB_BACKEND=s3` with R2's endpoint, bucket, `auto` region and path-style addressing, SMTP, OAuth; names and placeholders only), and a nightly backup (a systemd timer or cron example running `wardlockd backup` and uploading the copy with restic to a separate R2 bucket, keeping `WARDLOCK_SECRET` out of it). Since no image is published before v0.1.0, also add a Dockerfile target (or a documented path) that builds `wardlockd` and the dashboard from source, so the stack can run before the first release. Add a DEPLOYMENT section walking through it: firewall (22, 80, 443), DNS records (dashboard, wildcard and `auth` for sites, all DNS-only, not proxied), a Cloudflare API token scoped to DNS edit on the sites zone, R2 buckets and a token scoped to them, first start and the setup URL, custom domains (Wardlock certifies them on demand; Let's Encrypt rate limits; `WARDLOCK_ACME_CA` as a fallback), upgrades (back up, change the tag, `docker compose pull && up -d`) and restore. Nothing is provisioned, deployed or published.
- Acceptance: `docker compose -f deploy/vps/compose.yaml config` validates; the source-build image builds and starts locally in `local` mode against the example env with placeholders replaced (commands and output in the PR); `pnpm check` (including the docs-site link check) and `git diff --check` pass; no real hosts, tokens or addresses in examples; nothing provisioned or deployed.
- Skills: wardlock-item
- Parallel: Wave 12, beside C1.10. Owns `deploy/vps/`, `deploy/Dockerfile` (adding a target without changing the release image) and the new DEPLOYMENT section; no gates.

### O4.13: Acceptable use policy and abuse procedure

- Status: `todo`
- Owner:
- PR:
- Depends on: A2.13
- Scope: Draft an acceptable use policy for instances (`docs/ACCEPTABLE-USE.md`): no illegal, obscene or sexual content, nothing involving minors, no malware or phishing, the operator's right to screen (A2.15, when enabled, including that content may be sent to OpenAI's moderation service), remove content and suspend accounts. Link it from the setup and invitation pages, so a new owner sees it before their account exists (no checkbox; continuing means accepting), and from README and SECURITY. Write the operator's abuse procedure in DEPLOYMENT: triage reports, take down, preserve and report illegal material (in New Zealand, to the Department of Internal Affairs or the police) rather than deleting it, and contact the owner. Marked as a draft for the maintainer's legal review.
- Acceptance: Policy and procedure merged and linked; the setup and invitation pages show the link (tests, and checked at 1440px and 390px in light and dark); docs-site link check passes; `pnpm check` passes.
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 13, beside A2.14 or A2.15. Owns the new doc and the link on the two pages; no gates.

### O4.14: Documentation restructure

- Status: `todo`
- Owner:
- PR:
- Depends on: C1.10
- Scope: Restructure every hand-written Markdown file in the style of the CubieCode Counterstrain repository: each section leads with what most readers need (a few sentences, a short list or a table), and the dense material (implementation detail, edge cases, exact limits and their rationale, protocol and storage internals, threat-by-threat reasoning) moves into collapsed `<details><summary>…</summary>` blocks, with nothing deleted that is still true. Files: README (short: status line, a documentation index, a "what it does" list, a stack table, grouped command tables; Orca worktree and licensing FAQ detail collapsed or moved to CONTRIBUTING), ROADMAP (a status board at the top generated by a new `scripts/roadmap-board.mjs` from each item's Status and PR fields, with `pnpm check` failing when it is stale, as in Counterstrain's `tools/roadmap-board.py`; finished items and waves folded into collapsed blocks; open items kept expanded), docs/SPEC.md, docs/ARCHITECTURE.md (a built-items table in a collapsed block, as Counterstrain's "What each built item added"), docs/DEPLOYMENT.md (a quick-start path first, then reference sections whose variable tables and edge cases collapse), docs/SECURITY-MODEL.md (each threat: the defence in one or two sentences, the reasoning collapsed), docs/MODULES.md, docs/DESIGN-SYSTEM.md, docs/LAUNCH-CHECKLIST.md (open lines first, done lines collapsed), AGENTS.md, CONTRIBUTING.md, SECURITY.md, DESIGN.md, `docs-site/README.md`, `fixtures/README.md` and `deploy/vps/README.md`. Not in scope: the agent skills under `.agents/skills/` (vendored or read by agents), CLA.md (legal text), generated files and test data. Keep every heading anchor that other docs, the API contract, code comments, skills or the docs site link to, or update every link. Add `<details>` styling to `docs-site/src/style.css` (light and dark) and make the link checker and table of contents work with collapsed sections. Add a short "Docs style" rule to AGENTS.md so later items keep it, and tell workers there to run the board script after changing a Status line. No behaviour changes.
- Acceptance: Every page is shorter to scan (state before and after line counts of text outside `<details>`); no fact removed (diff reviewed section by section in the PR); `pnpm docs:build` and its link check pass; the docs site checked at 1440px and 390px in light and dark with collapsed and expanded blocks; `pnpm check` and `git diff --check` pass.
- Skills: wardlock-item, wardlock-web
- Parallel: Wave 12, alone, after C1.10 merges and before C1.12, O4.10 and wave 13 (it rewrites the files they all edit). Owns every Markdown file listed, `scripts/roadmap-board.mjs`, its `pnpm check` step and `docs-site/src/style.css`. Meanwhile, workers in other lanes touch only their own item's Status, Owner and PR fields.

## Waves

Suggested batches, based on the file ownership above. Re-check overlap before launching; merge in the order listed within a wave when items share interfaces.

1. **Wave 1:** F0.2, F0.3, F0.4, O4.1. F0.3 is the migration and lockfile owner; F0.2 owns the API contract; F0.4 owns `internal/mail`; O4.1 owns `deploy/` and release config.
2. **Wave 2** (after F0.2 and F0.3): C1.1, C1.2, C1.3, C1.4. C1.4 is the lockfile owner; no migrations expected. Merge C1.2 before C1.3 (C1.3 consumes `BlobStore` and `Notifier`).
3. **Wave 3:** C1.6, C1.7, A2.1, A2.8, with D3.1 as soon as a worker frees up (it is the only UI lane). C1.6 is the lockfile owner and, if needed, the migration owner. A2.1 owns the access cookie gate.
4. **Wave 4:** C1.5, A2.2, D3.2, C1.8. API additions for A2.2 agreed before the wave; C1.5 merges before A2.2's CLI commands.
5. **Wave 5:** A2.3, A2.4, A2.6, C1.9 (O4.2 after C1.9). A2.3 lands the shared login page template first.
6. **Wave 6:** A2.5, D3.3, D3.4, D3.5. Only one of D3.3, D3.4 and D3.5 changes `packages/ui`.
7. **Then:** A2.7 alone; O4.3 when its dependencies are done; O4.4 last.
8. **Wave 9:** A2.9, A2.10, A2.11, O4.5. A2.11 holds the API contract gate (`openapi.yaml` and generated clients); no migrations or lockfile changes expected. A2.9 agrees its limiter design with the coordinator before implementing it.
9. **Wave 10:** O4.6, then O4.7. Docs only; no gates.
10. **Wave 11:** O4.8 and O4.9, in either order. Lint and CI configuration only; no gates.
11. **Wave 12:** first D3.6, A2.12 and O4.11 (A2.12 holds the API contract gate; D3.6 merges first, since both touch the site page); then C1.10 (migration owner, takes the API contract gate) beside C1.11 (lockfile owner for `apps/server/go.sum`) and O4.12; then O4.14 alone after C1.10 (it rewrites the docs every later item edits); then C1.12; then O4.10 last.
12. **Wave 13:** A2.13 (migration owner), then A2.14 (migration owner, API contract gate), then A2.15 (migration owner); O4.13 beside A2.14 or A2.15. Starts after wave 12.

## Not planned yet

These are recorded so nobody starts them. Each needs a roadmap item and an explicit assignment first.

- MCP server for creating and managing links from AI tools.
- Teams and members: shared ownership of sites, roles.
- Scale-out: Postgres repositories passing the same contract tests, shared certmagic storage, a shared rate limiter, multi-instance cache invalidation.
- Hosted service concerns: billing, quotas, abuse reporting and scanning.
- Homebrew tap and other package managers.

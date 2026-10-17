# Wardlock

Wardlock ([wardlock.nz](https://wardlock.nz)) is a free, open-source, self-hostable way to publish static sites with access control built in. Deploy a folder to a subdomain with one command, then decide who can open it: anyone, people holding a share link, people who know a password, people who can receive an email code at an allowed address, or people who sign in with GitHub, Google or your own OIDC provider.

It is pronounced "ward-lock", not "warlock". A warded lock has internal obstructions, called wards, that only a key cut to the right shape can pass.

> **Status: pre-alpha.** This repository holds the foundation only. The server starts and serves a dashboard placeholder, a built-in hello-world site and health endpoints; the CLI prints its version. Deploys, share links and every other feature below are planned in the [roadmap](ROADMAP.md) and are not built yet. Do not use Wardlock for anything real.

## What it will do

- **Deploy.** `wardlock deploy ./dist docs.example.com` uploads only the files that changed, then switches the site over atomically. Every deploy can be rolled back instantly. TLS certificates are automatic, including wildcards and custom domains.
- **Share links.** The headline feature: a URL carrying a token (`?t=…`) that opens the page immediately, with no login screen. Links can expire or never expire, have a use limit, carry a label ("Linear ENG-123", "sent to Alice"), be limited to a path, and be revoked or rotated at any time.
- **Other policies.** Public, password, one-time email code (with address and domain allowlists) and OAuth/OIDC (with email, domain or organisation allowlists), per site or per path.
- **AI-readable.** Share links work for AI fetch tools, `curl` and link-preview bots, which keep no cookies and run no JavaScript. Every page is also available as Markdown or plain text with `?format=md` or `?format=txt`.
- **Self-hosting first.** One server binary and SQLite. No other required services; Docker is optional.

It is built for agencies and freelancers sending client previews, teams sharing internal reports and docs, and anyone sharing AI-generated HTML reports who wants a permanent link to put in Linear or similar tools.

## Quick start (development)

You need Go 1.27 (the build pins go1.27.2 and Go downloads it if needed), Node 24 (`.nvmrc`) and pnpm 12 (Corepack or `npm install -g pnpm`). Docker is not required.

```sh
pnpm install --frozen-lockfile
pnpm check          # the first run builds pinned Go tools into .local/bin
cp .env.example .env
pnpm dev
```

Then open:

- <http://wardlock.localhost:8080> for the dashboard placeholder;
- <http://hello.wardlock.localhost:8080> for the built-in hello-world site;
- <http://wardlock.localhost:8080/healthz> for health.

`*.localhost` resolves to your own machine, so subdomain routing works without DNS. To build the binaries instead, run `pnpm build` and start `.local/bin/wardlockd serve`; `.local/bin/wardlock --help` shows the CLI.

The Go modules use vanity import paths on `wardlock.nz`, which does not serve `go-import` metadata yet. Local builds work because `go.work` lists every module and each consuming `go.mod` has a `replace` pointing at `packages/apiclient-go`. See [deployment](docs/DEPLOYMENT.md#go-import-paths).

## Commands

| Command             | Purpose                                                         |
| ------------------- | --------------------------------------------------------------- |
| `pnpm dev`          | Run wardlockd with the dashboard's Vite dev server proxied      |
| `pnpm check`        | Every check: lint, types, tests, builds, codegen drift, Go      |
| `pnpm build`        | Build the dashboard and `.local/bin/wardlockd`, `wardlock`      |
| `pnpm codegen`      | Regenerate OpenAPI bindings (Go, TS) and sqlc queries           |
| `pnpm format`       | Apply Prettier and gofmt                                        |
| `pnpm browser:test` | Playwright acceptance against a real server, desktop and mobile |
| `pnpm test:watch`   | Vitest in watch mode                                            |

## Repository layout

```text
apps/server/          wardlockd: edge, owner API, embedded dashboard (Go, AGPL)
apps/cli/             wardlock command-line client (Go, MIT)
apps/web/             React + Vite dashboard (AGPL)
packages/api-spec/    OpenAPI 3.1 contract and generation script (MIT)
packages/apiclient-go Generated Go client (MIT)
packages/apiclient-ts Generated TypeScript client (MIT)
packages/ui/          Design system: tokens and components (AGPL)
deploy/               Dockerfile, compose, systemd and reverse-proxy examples
fixtures/             Synthetic sample sites for tests
scripts/              Checks, codegen, dev runner, worktree setup
tools/                Pinned Go tool versions (sqlc, oapi-codegen, linters)
docs/                 Spec, architecture, modules, security model, deployment
.agents/skills/       Agent skills; .claude/skills links here
```

Read [SPEC](docs/SPEC.md), [ARCHITECTURE](docs/ARCHITECTURE.md), [MODULES](docs/MODULES.md), [SECURITY-MODEL](docs/SECURITY-MODEL.md), [DEPLOYMENT](docs/DEPLOYMENT.md), [DESIGN-SYSTEM](docs/DESIGN-SYSTEM.md), [DESIGN](DESIGN.md), [ROADMAP](ROADMAP.md) and [AGENTS](AGENTS.md).

## Orca worktrees

`orca.yaml` runs `scripts/worktree-setup.sh` for each new worktree. It writes a lane-specific `.env` once (ports derived from the worktree path, a fresh `WARDLOCK_SECRET`, data in `.local/data`), installs locked dependencies, downloads Go modules and runs `pnpm check`. It copies nothing from the main checkout and starts no services. If a port is taken, edit that worktree's `.env`. Use the `wardlock-item` skill for one roadmap item and `wardlock-orchestrate` for coordinated parallel work.

## Licensing

Wardlock is split-licensed; see [LICENSE](LICENSE):

- **AGPL-3.0-only:** the server (`apps/server`), the dashboard (`apps/web`) and the design system (`packages/ui`).
- **MIT:** the CLI (`apps/cli`), the API contract (`packages/api-spec`) and the generated clients (`packages/apiclient-go`, `packages/apiclient-ts`), plus docs, scripts and deployment examples.

Common questions:

- **Do the sites I deploy fall under the AGPL?** No. Sites you publish are your content; the server's licence does not apply to them.
- **I run Wardlock unmodified. Do I owe anything?** No. Running an unmodified server, for yourself or for others, requires nothing of you.
- **When does the AGPL matter?** Only if you modify the server or dashboard and let others use your modified version over a network. Then you must offer those users the source of your modifications.
- **Can I build my own tools on the API?** Yes. The contract and clients are MIT, so integrations, scripts and CI tooling can use any licence.

Contributions need a signed CLA; see [CONTRIBUTING](CONTRIBUTING.md). Report vulnerabilities privately; see [SECURITY](SECURITY.md).

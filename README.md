# Wardlock

[![CI](https://github.com/CubieCode/wardlock/actions/workflows/ci.yml/badge.svg)](https://github.com/CubieCode/wardlock/actions/workflows/ci.yml)
[![Docs site](https://github.com/CubieCode/wardlock/actions/workflows/docs-site.yml/badge.svg)](https://github.com/CubieCode/wardlock/actions/workflows/docs-site.yml)

Wardlock ([wardlock.nz](https://wardlock.nz)) is a free, open-source, self-hostable way to publish static sites with access control built in. Deploy a folder to a subdomain with one command, then decide who can open it: anyone, people holding a share link, people who know a password, people who can receive an email code at an allowed address, or people who sign in with GitHub, Google or your own OIDC provider.

It is pronounced "ward-lock", not "warlock". A warded lock has internal obstructions, called wards, that only a key cut to the right shape can pass.

> **Status: alpha.** Every feature below is built, but Wardlock has not had an external security review: do not use it to protect anything sensitive yet, back up before upgrading, and expect changes before v1. See [SECURITY](SECURITY.md) to report a problem and the [roadmap](ROADMAP.md) for what is and is not planned.

## Documentation

- [SPEC](docs/SPEC.md): what Wardlock does, rule by rule: sites, deploys, policies, share links, sign-in methods, renditions and the CLI.
- [DEPLOYMENT](docs/DEPLOYMENT.md): running your own instance: DNS, TLS, systemd, Docker, reverse proxies, SMTP, OAuth, backups and upgrades.
- [SECURITY-MODEL](docs/SECURITY-MODEL.md): what Wardlock defends against, how, and which risks it accepts.
- [ACCEPTABLE-USE](docs/ACCEPTABLE-USE.md): what owners may not publish, how operators screen and enforce it, and how to report abuse (a draft awaiting legal review).
- [ARCHITECTURE](docs/ARCHITECTURE.md): components, request flow, data model, upload protocol and what each built item added.
- [MODULES](docs/MODULES.md): package boundaries and how they are enforced.
- [DESIGN](DESIGN.md) and [DESIGN-SYSTEM](docs/DESIGN-SYSTEM.md): the dashboard's character, tokens and components.
- Release notes: [v0.2.0](docs/releases/v0.2.0.md) (path deploys, `wardlock pull`, the agent skill and upgrading) and [v0.1.0](docs/releases/v0.1.0.md) (the first public alpha: what it does, how to install it, and its known limitations).
- [ROADMAP](ROADMAP.md): every item with its status, and the [launch checklist](docs/LAUNCH-CHECKLIST.md) for the public alpha.
- [AGENTS](AGENTS.md) and [CONTRIBUTING](CONTRIBUTING.md): conventions for anyone changing the repository, human or agent.

The user-facing pages are also published as a static site built from `docs/` ([docs-site](docs-site/README.md)).

## Status

Alpha. Every v1 feature is built; the open items are fewer object-store calls, abuse controls for public instances and release work. The [ROADMAP status board](ROADMAP.md#status-board) has the live counts.

| Area                       | Built                                                                                                                                                               |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F0. Foundation             | Monorepo, API contract and codegen, data model and migrations, mail delivery                                                                                        |
| C1. Core hosting           | Owner sign-in and passkeys, deploy API, edge serving, TLS, CLI, API tokens, blob GC, custom domains, hashed-asset caching, S3-compatible storage                    |
| A2. Access control         | Policies, share links, password, email code, OAuth and OIDC, AI-readable renditions, access log and per-site access log, security review, limiter and log hardening |
| D3. Dashboard              | Sign-in, sites and deploys, share links, policies editor, custom domains, open site                                                                                 |
| O4. Operations and release | Release artefacts and publish job, health and backups, docs site, launch checklist, VPS deployment                                                                  |

## What it does

- **Deploy.** `wardlock deploy ./dist docs.example.com` uploads only the files that changed, then switches the site over atomically. Every deploy can be rolled back instantly. `wardlock pull docs.example.com` downloads any deploy's files back. TLS certificates are automatic, including wildcards and custom domains.
- **Share links.** The headline feature: a URL carrying a token (`?t=…`) that opens the page immediately, with no login screen. Links can expire or never expire, have a use limit, carry a label ("Linear ENG-123", "sent to Alice"), be limited to a path, and be revoked or rotated at any time.
- **Other policies.** Public, password, one-time email code (with address and domain allowlists) and OAuth/OIDC (with email, domain or organisation allowlists), per site or per path.
- **AI-readable.** Share links work for AI fetch tools, `curl` and link-preview bots, which keep no cookies and run no JavaScript. Every page is also available as Markdown or plain text with `?format=md` or `?format=txt`.
- **Self-hosting first.** One server binary and SQLite. No other required services; Docker is optional.

It is built for agencies and freelancers sending client previews, teams sharing internal reports and docs, and anyone sharing AI-generated HTML reports who wants a permanent link to put in Linear or similar tools.

## Agent skill

[![skills.sh](https://skills.sh/b/CubieCode/wardlock)](https://skills.sh/CubieCode/wardlock)

`wardlock-publish` lets a coding agent publish a page it generated (a mockup, prototype or report) privately under one of your sites and hand you a share link that opens only that page. It needs the `wardlock` CLI, logged in to your server, both v0.2.0 or later.

```sh
npx skills add CubieCode/wardlock --skill wardlock-publish
```

<details>
<summary>What the skill does</summary>

- Checks that `wardlock` is installed and logged in, and asks you to run `wardlock login` rather than logging in itself.
- Publishes only to a protected root site. For a new one it deploys a placeholder, protects the site with share links, and only then deploys content, so nothing is ever public.
- Deploys each page into its own path (`/<slug>/`) with `wardlock deploy --path`, never overwriting another page unless you ask, and creates a share link scoped to that path. It shows you the link once and never writes it to a file or log.
- Lists, updates and removes the pages it published. It does not generate content.

The skill is [`skills/wardlock-publish`](skills/wardlock-publish/SKILL.md), MIT under [LICENSE](LICENSE). The repository's own skills in `.agents/skills/` are for working on Wardlock and are not offered for installation.

</details>

## Stack

| Area      | Choice                                                                                                                |
| --------- | --------------------------------------------------------------------------------------------------------------------- |
| Server    | Go 1.27, one binary (`wardlockd`) with the dashboard embedded                                                         |
| Storage   | SQLite (`modernc.org/sqlite`, no cgo) behind repository interfaces; file bodies on disk or in an S3-compatible bucket |
| TLS       | certmagic: automatic certificates, wildcards through DNS providers, on-demand for custom domains                      |
| API       | OpenAPI 3.1 contract; generated Go server, Go client and TypeScript client (oapi-codegen); sqlc for queries           |
| Dashboard | React 19 and Vite, built on the `@wardlock/ui` design system                                                          |
| CLI       | Go (`wardlock`), using only the generated Go client                                                                   |
| Tooling   | pnpm 12 and Turborepo, Node 24, ESLint, Prettier, Vitest, Playwright, golangci-lint, govulncheck                      |

## Quick start (development)

You need Go 1.27 (the build pins go1.27.3 and Go downloads it if needed), Node 24 (`.nvmrc`) and pnpm 12 (Corepack or `npm install -g pnpm`). Docker is not required.

```sh
pnpm install --frozen-lockfile
pnpm check          # the first run builds pinned Go tools into .local/bin
cp .env.example .env
pnpm dev
```

Then open:

- <http://wardlock.localhost:8080> for the dashboard (open the setup URL from the log first);
- `http://<name>.wardlock.localhost:8080` for a site once one of its deploys is active;
- <http://wardlock.localhost:8080/healthz> for health.

`*.localhost` resolves to your own machine, so subdomain routing works without DNS. To build the binaries instead, run `pnpm build` and start `.local/bin/wardlockd serve`; `.local/bin/wardlock --help` shows the CLI.

<details>
<summary>Go import paths</summary>

The Go modules use vanity import paths on `wardlock.nz`, which serves their `go-import` metadata from [`home-site/`](home-site/README.md). Local builds use the workspace: `go.work` lists every module and each consuming `go.mod` has a `replace` pointing at `packages/apiclient-go`. See [deployment](docs/DEPLOYMENT.md#go-import-paths).

</details>

## Commands

### Develop

| Command           | Purpose                                                    |
| ----------------- | ---------------------------------------------------------- |
| `pnpm dev`        | Run wardlockd with the dashboard's Vite dev server proxied |
| `pnpm codegen`    | Regenerate OpenAPI bindings (Go, TS) and sqlc queries      |
| `pnpm format`     | Apply Prettier and gofmt                                   |
| `pnpm test:watch` | Vitest in watch mode                                       |

### Check and test

| Command             | Purpose                                                                   |
| ------------------- | ------------------------------------------------------------------------- |
| `pnpm check`        | Every check: lint, roadmap board, types, tests, builds, codegen drift, Go |
| `pnpm browser:test` | Playwright acceptance against a real server, desktop and mobile           |

### Build

| Command           | Purpose                                                    |
| ----------------- | ---------------------------------------------------------- |
| `pnpm build`      | Build the dashboard and `.local/bin/wardlockd`, `wardlock` |
| `pnpm docs:build` | Build the docs site from `docs/` and check its links       |

### Repository tooling

| Command              | Purpose                                                                      |
| -------------------- | ---------------------------------------------------------------------------- |
| `pnpm roadmap:board` | Refresh ROADMAP's status board and fold finished items after a Status change |
| `pnpm check:roadmap` | Fail if the board is stale (part of `pnpm check`)                            |

## Repository layout

The server, CLI and dashboard live under `apps/`, the API contract, generated clients and design system under `packages/`, and the docs under `docs/`, with the docs site built from them in `docs-site/`.

<details>
<summary>Every top-level directory</summary>

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
docs-site/            Static docs site generated from docs/ (MIT)
skills/               Public agent skill: wardlock-publish (MIT)
.agents/skills/       Agent skills for working on the repository; .claude/skills links here
```

</details>

## Contributing

Read [AGENTS](AGENTS.md) and [CONTRIBUTING](CONTRIBUTING.md) before changing anything. Roadmap work runs in Orca worktrees, one lane per item; CONTRIBUTING explains the setup. Contributions need a signed CLA; see [CONTRIBUTING](CONTRIBUTING.md). Report vulnerabilities privately; see [SECURITY](SECURITY.md).

## Licensing

Wardlock is split-licensed; see [LICENSE](LICENSE):

- **AGPL-3.0-only:** the server (`apps/server`), the dashboard (`apps/web`) and the design system (`packages/ui`).
- **MIT:** the CLI (`apps/cli`), the API contract (`packages/api-spec`) and the generated clients (`packages/apiclient-go`, `packages/apiclient-ts`), plus docs, scripts and deployment examples.

<details>
<summary>Common questions</summary>

- **Do the sites I deploy fall under the AGPL?** No. Sites you publish are your content; the server's licence does not apply to them.
- **I run Wardlock unmodified. Do I owe anything?** No. Running an unmodified server, for yourself or for others, requires nothing of you.
- **When does the AGPL matter?** Only if you modify the server or dashboard and let others use your modified version over a network. Then you must offer those users the source of your modifications.
- **Can I build my own tools on the API?** Yes. The contract and clients are MIT, so integrations, scripts and CI tooling can use any licence.

</details>

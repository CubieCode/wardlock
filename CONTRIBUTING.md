# Contributing

Thanks for your interest in Wardlock. It is alpha software and the roadmap is driven by the maintainers. For now only collaborators can open pull requests: please open an issue to report a bug or suggest a change. Outside pull requests open once the CLA process below is in place.

## Contributor licence agreement

Before we can merge a pull request from anyone outside CubieCode, you must sign the [Wardlock Contributor Licence Agreement](CLA.md) through [CLA Assistant](https://cla-assistant.io). The bot comments on your first PR with a link; signing once covers all future contributions.

<details>
<summary>Why a CLA</summary>

Wardlock is split between AGPL-3.0 (server, dashboard, design system) and MIT (CLI, API contract, clients). The CLA lets CubieCode keep that split coherent and relicense parts of the project in future (for example moving a package from AGPL to MIT) without tracking down every past contributor. You keep the copyright in your contribution.

</details>

## Development

Follow the quick start in [README](README.md). Read [AGENTS](AGENTS.md) for conventions; they apply to people as well as coding agents.

- `pnpm check` must pass and `git diff --check` must be clean. It runs every linter, test, build, the codegen drift check and the Go checks.
- Change behaviour, docs and tests together. API changes start in `packages/api-spec/openapi.yaml`, followed by `pnpm codegen`.
- New source files need an SPDX header matching their directory's licence (see [LICENSE](LICENSE)); `pnpm check` tells you if one is missing.
- Never commit secrets, real tokens, real email addresses or customer content, including in tests, fixtures and screenshots.

## Roadmap work in Orca worktrees

Roadmap items run in Orca worktrees, one lane per item, each with its own `.env`, ports and data directory. Use the `wardlock-item` skill for one roadmap item and `wardlock-orchestrate` for coordinated parallel work. After changing an item's Status line, run `pnpm roadmap:board` to refresh ROADMAP's status board; `pnpm check` fails while it is stale.

<details>
<summary>What worktree setup does</summary>

`orca.yaml` runs `scripts/worktree-setup.sh` for each new worktree. It writes a lane-specific `.env` once (ports derived from the worktree path, a fresh `WARDLOCK_SECRET`, data in `.local/data`), installs locked dependencies, downloads Go modules and runs `pnpm check`. It copies nothing from the main checkout and starts no services. If a port is taken, edit that worktree's `.env`. Use the `wardlock-item` skill for one roadmap item and `wardlock-orchestrate` for coordinated parallel work.

</details>

## Commits and pull requests

- Branches: `feat/<roadmap-id>-<slug>` for roadmap items (`feat/c1.2-deploy-api`), `fix/<slug>` or `docs/<slug>` otherwise.
- Commit messages: a short imperative summary line (≤72 characters), a blank line, then what changed and why if it is not obvious. Roadmap items use `<ID>: <result>`. Keep commits focused; do not bypass hooks with `--no-verify`.
- PR titles: `<ID>: <title>` for roadmap items, otherwise a plain summary. The description covers what changed, how you validated it (commands and results), any open decisions and, for UI changes, screenshots at desktop and mobile widths with synthetic data.
- One logical change per PR. Rebase on `main` rather than merging it in.

## Reporting security issues

Do not open a public issue. See [SECURITY](SECURITY.md).

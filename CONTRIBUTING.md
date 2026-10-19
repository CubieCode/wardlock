# Contributing

Thanks for your interest in Wardlock. It is alpha software and the roadmap is driven by the maintainers, so please open an issue to discuss a change before writing a large PR.

## Contributor licence agreement

Before we can merge a pull request from anyone outside CubieCode, you must sign the Wardlock Contributor Licence Agreement through [CLA Assistant](https://cla-assistant.io). The bot comments on your first PR with a link; signing once covers all future contributions.

Why: Wardlock is split between AGPL-3.0 (server, dashboard, design system) and MIT (CLI, API contract, clients). The CLA lets CubieCode keep that split coherent and relicense parts of the project in future (for example moving a package from AGPL to MIT) without tracking down every past contributor. You keep the copyright in your contribution.

## Development

Follow the quick start in [README](README.md). Read [AGENTS](AGENTS.md) for conventions; they apply to people as well as coding agents.

- `pnpm check` must pass and `git diff --check` must be clean. It runs every linter, test, build, the codegen drift check and the Go checks.
- Change behaviour, docs and tests together. API changes start in `packages/api-spec/openapi.yaml`, followed by `pnpm codegen`.
- New source files need an SPDX header matching their directory's licence (see [LICENSE](LICENSE)); `pnpm check` tells you if one is missing.
- Never commit secrets, real tokens, real email addresses or customer content, including in tests, fixtures and screenshots.

## Commits and pull requests

- Branches: `feat/<roadmap-id>-<slug>` for roadmap items (`feat/c1.2-deploy-api`), `fix/<slug>` or `docs/<slug>` otherwise.
- Commit messages: a short imperative summary line (≤72 characters), a blank line, then what changed and why if it is not obvious. Roadmap items use `<ID>: <result>`. Keep commits focused; do not bypass hooks with `--no-verify`.
- PR titles: `<ID>: <title>` for roadmap items, otherwise a plain summary. The description covers what changed, how you validated it (commands and results), any open decisions and, for UI changes, screenshots at desktop and mobile widths with synthetic data.
- One logical change per PR. Rebase on `main` rather than merging it in.

## Reporting security issues

Do not open a public issue. See [SECURITY](SECURITY.md).

# Public alpha launch checklist

What must be true before Wardlock's repository goes public and the first alpha release (`v0.1.0`) is published. Each section lists its open lines first; the lines already done are collapsed beneath them. Every launch action below is still to be taken by its owner, and the checks marked _done_ must be re-run on the release commit before launching.

<details>
<summary>How this checklist was made and checked</summary>

This is a checklist only (roadmap item O4.4): writing it published nothing, pushed no tags and changed no DNS, repository settings, apps or deployments.

First checked against `main` at `41e4817` on 2026-10-19 (O4.4) and re-checked after wave 9 by O4.6 on 2026-10-19 with local commands and read-only requests (`curl`, `dig`, `gh api` GETs). Re-run the checks marked _done_ on the release commit before launching, since they describe this commit only. File and line references in the Evidence columns point at that commit; O4.14 later restructured the Markdown files, so find a cited passage in a document by its heading rather than its line number.

</details>

- **State:** _done_ (verified here), _open_ (needs work or an action) or _not verifiable from here_ (needs access or a state that does not exist yet).
- **Owner:** the person who takes the action or signs the line off. Every line, including those verified here, is signed off by the maintainer.

## Decisions for the maintainer

These lines need a decision rather than work. The recommendation is the checklist author's; the decision is @OhYea777's.

| Decision                              | Recommendation                                                                                                                   | Owner     | State |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------- | ----- |
| Sign release artefacts for the alpha? | Optional for the alpha: checksums and SBOMs exist, nothing is signed. Consider GitHub artefact attestations or cosign before v1. | @OhYea777 | open  |

## Licence and CLA

| Check                                                            | Owner     | State | Evidence                                                                                                                                                                                                                                                                                                                                                                                                             |
| ---------------------------------------------------------------- | --------- | ----- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The CLA text itself exists (CLA Assistant serves it from a gist) | @OhYea777 | open  | O4.7 drafted [`CLA.md`](../CLA.md), linked from CONTRIBUTING: CubieCode as the recipient, copyright licence with the right to sublicense and relicense, patent licence, the contributor keeping copyright, representations, organisations. Still to do: review it (ideally with a lawyer), settle the registered entity name and governing law, remove the draft line, then copy it to the gist CLA Assistant serves |
| CLA Assistant is enabled on the repository                       | @OhYea777 | open  | `gh api repos/CubieCode/wardlock/hooks` returns `[]` (no CLA Assistant webhook) and no CLA status appears on `main`'s checks. Enable it at cla-assistant.io with the gist above, then open a test PR from an account outside CubieCode                                                                                                                                                                               |

<details>
<summary>Done (8)</summary>

| Check                                                           | Owner     | State | Evidence                                                                                                                                                                                                                                                  |
| --------------------------------------------------------------- | --------- | ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AGPL/MIT split in `LICENSE` matches README "Licensing"          | @OhYea777 | done  | `LICENSE:3-15` and `README.md:78-83` list the same areas: AGPL-3.0-only for `apps/server`, `apps/web`, `packages/ui`; MIT for `apps/cli`, `packages/api-spec`, `packages/apiclient-go`, `packages/apiclient-ts` and everything else                       |
| Each licensed directory carries its full licence text           | @OhYea777 | done  | `apps/server/LICENSE`, `apps/web/LICENSE`, `packages/ui/LICENSE` are the GNU AGPL; `apps/cli/LICENSE`, `packages/api-spec/LICENSE`, `packages/apiclient-go/LICENSE`, `packages/apiclient-ts/LICENSE` are MIT                                              |
| `package.json` licence fields match their area                  | @OhYea777 | done  | `@wardlock/web` and `@wardlock/ui` are `AGPL-3.0-only`; `@wardlock/api-spec`, `@wardlock/apiclient-ts` and `@wardlock/docs-site` are `MIT`; the root is `SEE LICENSE IN LICENSE`. All packages are `private`, so none can be published to npm by accident |
| `scripts/check-boundaries.mjs` licence and boundary checks pass | @OhYea777 | done  | `pnpm check` on `41e4817`: "Licence headers and boundaries OK (617 files)", exit 0                                                                                                                                                                        |
| Release archives ship the right licences                        | @OhYea777 | done  | `.goreleaser.yaml:59-71`: the wardlockd archive has `LICENSE` and `apps/server/LICENSE`, the wardlock archive `LICENSE` and `apps/cli/LICENSE`; the image is labelled `AGPL-3.0-only` (`.goreleaser.yaml:99`)                                             |
| GitHub shows the split licence sensibly                         | @OhYea777 | done  | `gh api repos/CubieCode/wardlock` reports licence `NOASSERTION`, expected for a split licence; the root `LICENSE` explains the split                                                                                                                      |
| CONTRIBUTING describes the CLA                                  | @OhYea777 | done  | `CONTRIBUTING.md:5-9`: CLA Assistant, signed once, why the CLA exists, copyright kept by the contributor                                                                                                                                                  |

</details>

## Security review

Every security review line is done.

<details>
<summary>Done (7)</summary>

| Check                                                              | Owner     | State | Evidence                                                                                                                                                                                                                                                                                   |
| ------------------------------------------------------------------ | --------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| SECURITY.md's reporting route works                                | @OhYea777 | done  | Private vulnerability reporting enabled 2026-10-21 after the repository went public (`gh api …/private-vulnerability-reporting`: `{"enabled":true}`), so the route `SECURITY.md` describes exists                                                                                          |
| A2.7 access security review merged                                 | @OhYea777 | done  | [PR #65](https://github.com/CubieCode/wardlock/pull/65) merged 2026-10-19; ROADMAP A2.7 `done`. It walked 108 SECURITY-MODEL claims, fixed one medium and several low findings, and left no open high-severity finding                                                                     |
| A2.9: sign-in limiter tables that resist flooding (**medium**)     | @OhYea777 | done  | [PR #72](https://github.com/CubieCode/wardlock/pull/72) merged; ROADMAP A2.9 `done`. Overflow sketches and per-network shares in `docs/SECURITY-MODEL.md` "Limiter tables under flooding", which also states the remaining bound (six networks can keep one site's method shut)            |
| A2.10: third-party log hygiene (**low**)                           | @OhYea777 | done  | [PR #71](https://github.com/CubieCode/wardlock/pull/71) merged; ROADMAP A2.10 `done`; `docs/SECURITY-MODEL.md` and `docs/DEPLOYMENT.md` describe the result                                                                                                                                |
| A2.11: distinct error code for the domain-check cooldown (**low**) | @OhYea777 | done  | [PR #70](https://github.com/CubieCode/wardlock/pull/70) merged; ROADMAP A2.11 `done`                                                                                                                                                                                                       |
| No known vulnerable dependencies                                   | @OhYea777 | done  | `pnpm check`: govulncheck "No vulnerabilities found" for every Go module. Dependabot alerts: 11 in total, all `fixed`, none open                                                                                                                                                           |
| SECURITY.md describes the alpha accurately                         | @OhYea777 | done  | `SECURITY.md:3` says alpha, no external review, not for anything sensitive (A2.7 was internal); scope and out-of-scope match SECURITY-MODEL; `SECURITY.md:21`: only `main` is supported until the first release. Change that line to "the latest alpha release" when `v0.1.0` is published |

</details>

## Release artefacts

O4.1 builds, per release: `wardlockd` and `wardlock` archives for linux, darwin and windows on amd64 and arm64 (12 archives, `.zip` on Windows), one SPDX JSON SBOM per archive, `checksums.txt` (SHA-256 of every archive and SBOM), and the multi-arch image `ghcr.io/cubiecode/wardlock` (linux/amd64 and linux/arm64, non-root, with an SBOM attestation when pushed). Nothing is signed. A GitHub release is created as a draft.

<details>
<summary>Verify a snapshot locally</summary>

Needs Docker with buildx; nothing is pushed or published:

```sh
GOWORK=off GOFLAGS= GOBIN="$PWD/.local/bin" go -C /tmp install github.com/goreleaser/goreleaser/v2@v2.18.3
GOWORK=off GOFLAGS= GOBIN="$PWD/.local/bin" go -C /tmp install github.com/anchore/syft/cmd/syft@v1.52.0
PATH="$PWD/.local/bin:$PATH" goreleaser release --snapshot --clean   # writes .local/goreleaser
bash scripts/release-smoke.sh
bash scripts/test-module-tags.sh
```

</details>

| Check                                 | Owner     | State                    | Evidence                                                                                                                                                                          |
| ------------------------------------- | --------- | ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Published release verified end to end | @OhYea777 | not verifiable from here | After publishing: `sh deploy/install.sh --version 0.1.0 --component all --dir <tmp>`, `docker run ghcr.io/cubiecode/wardlock:0.1.0`, and the checksums against the GitHub release |

<details>
<summary>Done (7)</summary>

| Check                                                           | Owner     | State | Evidence                                                                                                                                                                                                                                                                                                                                                                                                       |
| --------------------------------------------------------------- | --------- | ----- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Release notes                                                   | @OhYea777 | done  | O4.10: [`docs/releases/v0.1.0.md`](releases/v0.1.0.md), written by hand since changelog generation is off (`.goreleaser.yaml:108`): the alpha warning, what is built, the four install routes, first run, backup, upgrade and restore pointers into DEPLOYMENT, known limitations and how to report problems; linked from the README's documentation index. Add it to the draft GitHub release when publishing |
| Snapshot builds every artefact                                  | @OhYea777 | done  | Local `goreleaser release --snapshot --clean` (v2.18.3, syft v1.52.0) on `41e4817`: exit 0, version `0.0.1-snapshot.41e4817`, 12 archives, 12 `.sbom.json`, `checksums.txt`, images `…-amd64` and `…-arm64` loaded into the local daemon only                                                                                                                                                                  |
| Archives and SBOMs match `checksums.txt`; `install.sh` verifies | @OhYea777 | done  | `scripts/release-smoke.sh`: "12 archives, their SBOMs and checksums OK"; "install.sh installs verified binaries and refuses a bad checksum"                                                                                                                                                                                                                                                                    |
| Image runs as non-root and answers `/healthz`                   | @OhYea777 | done  | `scripts/release-smoke.sh`: "runs as uid 65532 and /healthz answers: {"status":"ok"}"                                                                                                                                                                                                                                                                                                                          |
| CI runs the same snapshot on every PR and on main               | @OhYea777 | done  | `Release` workflow on `41e4817`: `snapshot` success, `module-tags` success, `publish` skipped                                                                                                                                                                                                                                                                                                                  |
| Publishing is off until authorised                              | @OhYea777 | done  | O4.11: `publish` runs only for a pushed `v*` tag (`.github/workflows/release.yml:89`); `.goreleaser.yaml:85` and `:111` skip the image and release unless `WARDLOCK_PUBLISH=true`. No `v*` tag exists, so nothing can publish until one is pushed. `gh api …/releases` returns 0 releases and `…/tags` 0 tags                                                                                                  |
| Publish job ready to enable                                     | @OhYea777 | done  | O4.11: `release.yml:29` triggers on `v*` tags (not module tags); `publish` (`:89`) runs only for that tag push, logs in to ghcr.io with `docker login` (`:103`), alone has `contents: write` and `packages: write`, and creates a draft release. Pushing `v0.1.0` publishes; decide the ghcr.io package's visibility after the first push                                                                      |

</details>

## Go import paths and module tags

| Check                                                                    | Owner     | State                    | Evidence                                                                                                                                                                                                                                                      |
| ------------------------------------------------------------------------ | --------- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Release commit and module tags created                                   | @OhYea777 | open                     | Steps in `docs/DEPLOYMENT.md:320-322`: tag `packages/apiclient-go/v0.1.0`, drop the `replace` directives (`apps/server/go.mod:56`, `apps/cli/go.mod:24`) on the release commit, then tag `v0.1.0`, `apps/cli/v0.1.0`, `apps/server/v0.1.0`. No tags exist yet |
| `go install wardlock.nz/cli/cmd/wardlock@v0.1.0` works from the internet | @OhYea777 | not verifiable from here | Needs the meta tags, a public repository and the tags above; then check with `GOWORK=off GOFLAGS= go install …@v0.1.0` and confirm pkg.go.dev shows the MIT and AGPL licences                                                                                 |

<details>
<summary>Done (3)</summary>

| Check                                                                             | Owner     | State | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| --------------------------------------------------------------------------------- | --------- | ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Module paths are `wardlock.nz/server`, `wardlock.nz/cli`, `wardlock.nz/apiclient` | @OhYea777 | done  | `apps/server/go.mod:1`, `apps/cli/go.mod:1`, `packages/apiclient-go/go.mod:1`                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `wardlock.nz` serves `go-import` meta tags for all three modules                  | @OhYea777 | done  | Since 2026-10-21 from `home-site/` (site `home`, SPA fallback, custom domain `wardlock.nz`; [PR #112](https://github.com/CubieCode/wardlock/pull/112)). `curl 'https://wardlock.nz/{cli,cli/cmd/wardlock,server/cmd/wardlockd,apiclient}?go-get=1'`: 200 with all three tags; `GOPROXY=direct go list -m wardlock.nz/{apiclient,cli,server}@main` resolves each module. `scripts/home-site.test.mjs` checks the tags against `go.work`, and `scripts/test-module-tags.sh` installs through the page |
| Tag scheme works                                                                  | @OhYea777 | done  | `scripts/test-module-tags.sh` locally: `go install` of both commands at `v0.1.0-tagtest.1` works through a local `go-import` proxy; a tag on a commit with `replace` and an unprefixed tag are rejected. CI `module-tags` success on `41e4817`                                                                                                                                                                                                                                                      |

</details>

## Repository

| Check                          | Owner     | State | Evidence                                                                                                                                                                                                                                                                                                   |
| ------------------------------ | --------- | ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Outside pull requests accepted | @OhYea777 | open  | `gh api repos/CubieCode/wardlock`: `pull_request_creation_policy: collaborators_only`, so only users with write access can open pull requests (GitHub cannot turn forking off on a public repository). Set it back to `all` once CLA Assistant works (see Licence and CLA), and update CONTRIBUTING's lead |

<details>
<summary>Done (8)</summary>

| Check                                    | Owner     | State | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| ---------------------------------------- | --------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Repository is public                     | @OhYea777 | done  | Public since 2026-10-21. Before the flip, gitleaks v8.18.4 scanned all 174 commits on every ref: 21 findings, each read and each a synthetic test value (`sk-test-…`, `FakeKeyMaterialNotReal`, base64 of "test-only-secret-not-for-product", example IDs and the API spec's passkey examples); no `.env`, key or credential file was ever committed, and no real account ID, token, address or public IP appears. Commit author metadata carries the maintainer's own address |
| Private vulnerability reporting enabled  | @OhYea777 | done  | `gh api …/private-vulnerability-reporting`: `{"enabled":true}`                                                                                                                                                                                                                                                                                                                                                                                                                 |
| Branch protection or a ruleset on `main` | @OhYea777 | done  | Ruleset `main` (`gh api …/rules/branches/main`): pull request required (squash only, no approvals required), CI `check` and `browser`, `Release` `snapshot` and `module-tags`, `Docs site` `build` required from GitHub Actions, force-pushes and deletion blocked; repository admins may bypass only through a pull request. All five checks run on every pull request (no path filters)                                                                                      |
| Secret scanning and push protection      | @OhYea777 | done  | `security_and_analysis`: `secret_scanning` and `secret_scanning_push_protection` enabled                                                                                                                                                                                                                                                                                                                                                                                       |
| Public Actions settings                  | @OhYea777 | done  | `gh api …/actions/permissions/fork-pr-contributor-approval`: `all_external_contributors`. Default workflow token read-only, cannot approve pull requests. Self-hosted runners also refuse fork pull request jobs on the host, in a job-started hook outside the repository                                                                                                                                                                                                     |
| Merge settings                           | @OhYea777 | done  | Squash merge only, branches deleted on merge (`gh api repos/CubieCode/wardlock`)                                                                                                                                                                                                                                                                                                                                                                                               |
| Dependabot alerts and security updates   | @OhYea777 | done  | `gh api …/vulnerability-alerts`: 204 (enabled); `…/automated-security-fixes`: enabled, not paused; `.github/dependabot.yml` covers gomod (server, CLI, client, tools), npm and GitHub Actions weekly                                                                                                                                                                                                                                                                           |
| CI runners for outside pull requests     | @OhYea777 | done  | `ci.yml`, `release.yml` and `docs-site.yml` run fork PRs on `ubuntu-latest` only, never on the self-hosted runner (`vars.RUNNER`); workflows have `contents: read`                                                                                                                                                                                                                                                                                                             |

</details>

## Documentation

Every documentation line is done.

<details>
<summary>Done (7)</summary>

| Check                                    | Owner     | State | Evidence                                                                                                                                                                                                                                                                      |
| ---------------------------------------- | --------- | ----- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| README is accurate for the alpha         | @OhYea777 | done  | O4.6: `README.md:7` carries the alpha notice and "What it does" describes built features. README's "Go import paths" note says `wardlock.nz` serves the metadata (updated 2026-10-21)                                                                                         |
| CONTRIBUTING is accurate for the alpha   | @OhYea777 | done  | O4.6: `CONTRIBUTING.md:3` says alpha                                                                                                                                                                                                                                          |
| DEPLOYMENT is accurate for the alpha     | @OhYea777 | done  | O4.6: `docs/DEPLOYMENT.md:3` says alpha. Its statements that releases and images are not published stay true until the release; update them with it. "Go import paths" says `wardlock.nz` serves the metadata (updated 2026-10-21)                                            |
| SPEC is accurate for the alpha           | @OhYea777 | done  | A2.7 reconciled SPEC with the code (PR #65); O4.6 corrected the remaining stale statements and replaced the "built so far" list                                                                                                                                               |
| Docs site deployed                       | @OhYea777 | done  | Deployed 2026-10-21 by hand with `wardlock deploy` as site `docs` on the operator's instance, served at `docs.wardlock.nz`; the `Docs site` workflow's deploy job now publishes it from `main` with the `WARDLOCK_SERVER` variable and `WARDLOCK_TOKEN` secret (deploy scope) |
| Docs site builds with every link checked | @OhYea777 | done  | `pnpm check` includes `pnpm docs:build` and its link check, exit 0; `Docs site` workflow `build` on PRs                                                                                                                                                                       |
| Docs site carries an alpha warning       | @OhYea777 | done  | O4.6: the home page notice (`docs-site/src/build.mjs:56`) and every page's footer (`docs-site/src/render.mjs:187`) say alpha                                                                                                                                                  |
| Alpha warnings present                   | @OhYea777 | done  | `README.md:7`, `SECURITY.md:3`, `docs/DEPLOYMENT.md:3`, both docs-site pages above                                                                                                                                                                                            |

</details>

## Operations (O4.2)

All 5 checks in this section are done.

<details>
<summary>Done (5)</summary>

| Check                                     | Owner     | State | Evidence                                                                                                                                                                                                                                                   |
| ----------------------------------------- | --------- | ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Health and readiness documented           | @OhYea777 | done  | `docs/DEPLOYMENT.md:217-227`: `/healthz` on every host; `/readyz` (database, blobs, certificates; 5-second check timeout; `shutting_down` on drain) on the opt-in operator listener `WARDLOCK_OPERATOR_ADDR`, with the warning to keep it off the internet |
| Metrics and alerts documented             | @OhYea777 | done  | `docs/DEPLOYMENT.md:228-243`: Prometheus text format, labels never name hosts, paths, sites or owners, a scrape job and three suggested alerts                                                                                                             |
| Logs documented, with what is redacted    | @OhYea777 | done  | `docs/DEPLOYMENT.md:245-247`: JSON or text on stderr, request lines with credentials redacted, IDs only, and the deliberate exceptions (`setup_url`, `dev://` mail)                                                                                        |
| Backups and restore documented and tested | @OhYea777 | done  | `docs/DEPLOYMENT.md:249-277`: `wardlockd backup`, blob `rsync`, restore steps, keeping `WARDLOCK_SECRET`; `TestBackupAndRestore` in `apps/server/cmd/wardlockd` passes under `pnpm check`                                                                  |
| Upgrades documented                       | @OhYea777 | done  | `docs/DEPLOYMENT.md:279-281`: forward-only migrations on start, back up first                                                                                                                                                                              |

</details>

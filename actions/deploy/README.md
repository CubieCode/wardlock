# Wardlock deploy action

Deploys a static site to a [Wardlock](https://wardlock.nz) server from GitHub Actions. On a pull request it deploys to the preview `pr-<number>` instead (served at `<site>--pr-<number>.<base domain>`, under the site's own policies), keeps one comment on the pull request with the preview's address, and deletes the preview when the pull request closes.

```yaml
name: Site
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, closed]
permissions:
  contents: read
  pull-requests: write # for the preview comment
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: npm ci && npm run build # whatever builds your site
        if: github.event.action != 'closed'
      - uses: CubieCode/wardlock/actions/deploy@vX.Y.Z
        with:
          server: https://app.wardlock.example
          token: ${{ secrets.WARDLOCK_TOKEN }}
          directory: dist
          site: docs
```

The token is a Wardlock API token with only the `deploy` scope, stored as a repository secret. Create one on the dashboard's API tokens page or with `wardlock token create`; see [Deploying from CI](../../docs/DEPLOYMENT.md#deploying-from-ci). The action is MIT licensed ([LICENSE](LICENSE)), like the CLI.

## Inputs and outputs

| Input               | Default                            | Meaning                                                                                                                                  |
| ------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `server`            | `server` in `wardlock.json`        | The server's dashboard origin, such as `https://app.wardlock.example`.                                                                   |
| `token`             | required                           | A `deploy`-scoped API token from a secret. Masked before anything runs and passed to the CLI only as `WARDLOCK_TOKEN`.                   |
| `directory`         | `dir` in `wardlock.json`, else `.` | The directory to deploy.                                                                                                                 |
| `site`              | `site` in `wardlock.json`          | The site: `docs`, `docs.<base domain>` or a custom domain. Created on the first deploy.                                                  |
| `message`           | short commit SHA and subject       | The deploy message (at most 500 characters).                                                                                             |
| `path`              | none                               | Deploy into this path of the site only, such as `/mockup-x/` (a [path deploy](../../docs/SPEC.md#deploys)).                              |
| `preview`           | `pr-<number>` on pull requests     | Deploy to this preview instead of the live site. Names are 1 to 20 characters of `a-z`, `0-9` and `-`.                                   |
| `pr-preview`        | `true`                             | On `pull_request` events: deploy to `pr-<number>`, comment, and delete on close. `false` deploys to the live site as on any other event. |
| `delete`            | `false`                            | `true` deletes the preview (`preview`, or the pull request's) instead of deploying.                                                      |
| `working-directory` | `.`                                | Where the CLI runs and reads `wardlock.json`.                                                                                            |
| `version`           | the tag the action is used at      | The `wardlock` CLI release to download, such as `v0.4.0`. Needed when the action is pinned by commit SHA.                                |
| `cli-path`          | none                               | A `wardlock` binary to run instead of downloading one (this repository's own CI builds it from source).                                  |
| `github-token`      | `${{ github.token }}`              | The token that writes the pull request comment; it needs `pull-requests: write`. Empty skips the comment.                                |
| `github-api-url`    | `${{ github.api_url }}`            | The GitHub API the comment goes to (GitHub Enterprise Server sets this itself).                                                          |

| Output      | Value                                                                                       |
| ----------- | ------------------------------------------------------------------------------------------- |
| `url`       | The address the deploy is served at: the site, its path, or the preview. Empty on deletion. |
| `deploy-id` | The new deploy's ID. Empty on deletion.                                                     |
| `preview`   | The preview deployed to or deleted; empty for the live site.                                |

<details>
<summary>What each run does</summary>

1. Masks `token`, then decides: deploy to the live site, deploy to a preview, delete a preview, or skip (see [pull requests from forks](#pull-requests-from-forks)). A `pull_request` event deletes the preview when its action is `closed`; on that event there is nothing to build, so skip the build step as in the example above.
2. Without `cli-path`, downloads `wardlock_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) and `checksums.txt` from the GitHub release, and stops with an error unless the archive's SHA-256 matches its line in `checksums.txt`. Linux, macOS and Windows runners on x64 and arm64 are supported. The version is `version`, else the action's own tag; a run pinned to a branch or a commit SHA without `version` fails, saying so. The download needs `curl`, and the runner needs `jq` (both are on GitHub-hosted runners).
3. Runs `wardlock --json deploy` (or `wardlock --json preview delete … --yes`) in `working-directory`, so `wardlock.json` there supplies the server, site, directory, SPA fallback, redirects and headers the inputs leave out. Deleting a preview that is already gone succeeds with a notice.
4. On a pull request, creates or updates one comment with the preview's address, deploy ID and commit. The comment carries a hidden marker naming the site and preview and is only ever matched on comments written by a bot, so each preview has its own comment and nobody can make the action edit theirs. On close it notes the preview was deleted, if there is a comment. Without `pull-requests: write` the comment is skipped with a notice; any other API failure is a warning, since the deploy has already happened.

The default message uses the commit the run is for (the pull request's head commit on pull requests) and its subject from `git log`, falling back to the event's commit message or the pull request's title when the checkout does not hold that commit.

</details>

## Pull requests from forks

GitHub gives workflows for pull requests from forks (and from Dependabot) no secrets, so the action has no token. It then ends with a notice and deploys nothing; it never fails the run. Previews are for pull requests from branches of the same repository.

Never use `pull_request_target` to get round this. It runs with the base repository's secrets and a write token, so a workflow that checks out and builds the fork's code under it runs that code with your Wardlock token: the fork's build scripts, `package.json` hooks or a changed workflow step can read it and deploy anything to any of your sites, or keep it. The action warns when it runs on `pull_request_target`.

<details>
<summary>Previewing an outside contribution</summary>

Review the change, then push it to a branch of your own repository (for example with `gh pr checkout <number>` and `git push origin HEAD:refs/heads/preview-<number>`) and open a pull request from that branch: its workflow runs with your secrets on code you have read.

</details>

## Pinning and versions

Pin the action to a release tag, `CubieCode/wardlock/actions/deploy@vX.Y.Z`, and it downloads the CLI from the same release. Pinning by commit SHA is safer still; then set `version` to the release that commit belongs to:

```yaml
- uses: CubieCode/wardlock/actions/deploy@<commit sha> # vX.Y.Z
  with:
    version: vX.Y.Z
```

Previews need a server and CLI with preview deploys (C1.19); on an older server, deploy with `pr-preview: false` or only on `push`.

<details>
<summary>How this action is tested</summary>

The scripts in `scripts/` have unit tests in `test/` (run by `pnpm check` in this repository), including the comment step against a fake GitHub API and the download against a release whose checksum does not match. The [Deploy action workflow](../../.github/workflows/deploy-action.yml) runs the action from the working tree against a throwaway `wardlockd` on the runner for a live deploy, a preview deploy and a preview deletion, checks what the server serves, downloads the CLI from a local release (and fails on a tampered one), checks that the API token never appears in the job's log, and runs actionlint and shellcheck.

</details>

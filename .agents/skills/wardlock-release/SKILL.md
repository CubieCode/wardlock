---
name: wardlock-release
description: Cut a Wardlock release vX.Y.Z end to end, from release notes through the four tags, the release workflow and the draft GitHub release. Invoked by hand only; invoking it with a version is the release decision.
disable-model-invocation: true
argument-hint: X.Y.Z
allowed-tools: "Bash(git tag:*) Bash(git -C:*) Bash(git push origin:*) Bash(gh release:*) Bash(gh run:*) Bash(gh attestation:*)"
metadata:
  internal: true
---

# Release vX.Y.Z

Follow DEPLOYMENT "Releases and module tags" (docs/DEPLOYMENT.md). A release has one version and four tags: `packages/apiclient-go/vX.Y.Z` on main, then `apps/cli/vX.Y.Z`, `apps/server/vX.Y.Z` and `vX.Y.Z` on a release commit that drops the `replace` directives. Main keeps its `replace` directives; the release commit is never merged.

**Authority.** The user invoking this skill with a version is the explicit authorisation to push that version's tags. Without a version, propose one (minor for features or migrations, patch for fixes only, while below v1) with a one-paragraph reason and wait for the user's answer before tagging anything. The authority covers only these tags, the draft release and its notes. Publishing the draft, deploying the production instance and changing anything else still need their own yes.

Run every command from the main checkout with `git -C <repo>` and absolute paths. Use full SHAs in tag commands and in `gh run list -c`.

## 1. Preconditions

Stop and report if any fails:

- The main checkout is on `main`, clean and equal to `origin/main` after `git pull --ff-only`. The checks on its HEAD passed (`gh run list -c <sha>`, and the HEAD PR's checks).
- None of the four tags exists locally or on `origin` (`git ls-remote --tags origin '*vX.Y.Z'`).
- No open lane holds the lockfile gate for `apps/cli/go.sum` or `apps/server/go.sum`.
- Note the migrations added since the last release (`git diff --stat vPREV..HEAD -- apps/server/migrations`) and whether `packages/apiclient-go` changed. Every release tags the client, changed or not.

## 2. Release notes PR

Skip this step when `docs/releases/vX.Y.Z.md` is already on main and says vX.Y.Z throughout.

1. In a worktree on a branch `docs/vX.Y.Z-release-notes`, write `docs/releases/vX.Y.Z.md` in the previous release's format: intro (what the release is, and "back up before upgrading" when it adds migrations), the alpha warning, "What's new" (a table, then a `<details>` block with one bullet per change linking SPEC or DEPLOYMENT anchors), "Upgrading from vPREV" (backup, server, CLI, any optional step, then a `<details>` on mixing versions), "Install", "Known limitations" and "Reporting problems". Read SPEC for every claim; read `git log vPREV..HEAD` for the list.
2. Bump the previous version to the new one in `deploy/vps/compose.yaml` and in DEPLOYMENT's install and verify examples, and add the release to the README's release-notes line.
3. Check every relative link and `#anchor` resolves, run Prettier, then `git diff --check`. Commit "Release notes for vX.Y.Z", push, open the PR with `--body-file`, and `gh pr merge --squash --auto`.
4. Wait for CI. If it fails for a reason outside the PR (a new govulncheck advisory, say), fix that in its own PR first, since the release should carry the fix, and mention it in the notes. Then pull main and remove the worktree and branch.

## 3. Client tag

```sh
git -C <repo> tag -a packages/apiclient-go/vX.Y.Z <main sha> -m "wardlock.nz/apiclient vX.Y.Z"
git -C <repo> push origin packages/apiclient-go/vX.Y.Z
```

Never fetch `wardlock.nz/apiclient@vX.Y.Z` before this push: the Go module proxy caches "not found" for minutes.

## 4. Release commit and module tags

1. `git worktree add --detach ../wardlock-rel <main sha>`.
2. In `apps/cli/go.mod` and `apps/server/go.mod`, replace `wardlock.nz/apiclient v0.0.0-00010101000000-000000000000` with `wardlock.nz/apiclient vX.Y.Z`, and delete the `replace wardlock.nz/apiclient => ../../packages/apiclient-go` directive with its comment.
3. In each module, `GOWORK=off GOFLAGS=-mod=mod go mod tidy`, then `GOWORK=off go build ./...` and `GOWORK=off go vet ./...`. The diff should be the two `go.mod` files and two lines added to each `go.sum`.
4. Commit in the worktree:

   ```text
   Release vX.Y.Z

   Drop the development replace directives and require
   wardlock.nz/apiclient vX.Y.Z (tagged packages/apiclient-go/vX.Y.Z on the
   parent commit), so go install wardlock.nz/cli/cmd/wardlock@vX.Y.Z and
   wardlock.nz/server/cmd/wardlockd@vX.Y.Z work. main keeps the replace
   directives (docs/DEPLOYMENT.md, "Releases and module tags").
   ```

5. Tag that commit and push both tags together (module tags never start the publish job):

   ```sh
   git -C <repo> tag -a apps/cli/vX.Y.Z <release sha> -m "wardlock.nz/cli vX.Y.Z"
   git -C <repo> tag -a apps/server/vX.Y.Z <release sha> -m "wardlock.nz/server vX.Y.Z"
   git -C <repo> push origin apps/cli/vX.Y.Z apps/server/vX.Y.Z
   ```

6. Check `GOWORK=off GOBIN=<scratchpad>/gobin go install wardlock.nz/cli/cmd/wardlock@vX.Y.Z` and the same for `wardlock.nz/server/cmd/wardlockd`, then delete the binaries. Remove the worktree; the tags keep the commit.

## 5. The release tag

Push `vX.Y.Z` on its own. GitHub sends no push events when more than three tags are pushed at once, so a combined push can skip the release run.

```sh
git -C <repo> tag -a vX.Y.Z <release sha> -m "Wardlock vX.Y.Z"
git -C <repo> push origin vX.Y.Z
```

Find the run with `gh run list -c <release sha>` and watch it with `gh run watch <id> --exit-status` in the background. Its jobs are `snapshot`, `module-tags` and `publish`. If one fails, report the failed log and stop. Never delete or move a pushed tag without the user.

## 6. Draft release

1. Check `gh release view vX.Y.Z`: it is a draft with both binaries' archives for Linux, macOS and Windows on amd64 and arm64, an SBOM each, and `checksums.txt`.
2. In a new scratchpad directory, download `wardlockd_X.Y.Z_linux_amd64.tar.gz` and `checksums.txt`, then run `sha256sum --ignore-missing -c checksums.txt`, `gh attestation verify <archive> --repo CubieCode/wardlock` and `gh attestation verify oci://ghcr.io/cubiecode/wardlock:X.Y.Z --repo CubieCode/wardlock`.
3. Make the release body from `docs/releases/vX.Y.Z.md` at the tag. Drop the `# Wardlock vX.Y.Z` heading and rewrite every relative link as `https://github.com/CubieCode/wardlock/blob/vX.Y.Z/<path from the repo root>#anchor`. Write it to the scratchpad and run `gh release edit vX.Y.Z --draft=true --notes-file <file>`.
4. Report: the tags and SHAs, the run, what was verified, and the draft's URL. Ask whether to publish it. Only on a yes, run `gh release edit vX.Y.Z --draft=false`. Upgrading the production instance is a separate request.

## If a push is refused

The permission mode may refuse a tag push even after this skill is invoked. Do not retry it or work around it. Give the user the exact command as one line to run with `! `, then carry on from the next step once its output shows the tag was pushed.

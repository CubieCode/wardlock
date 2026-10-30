---
name: wardlock-item
description: Implement one Wardlock roadmap item in an Orca worktree, from claiming it through tests, status and PR. Use only for an explicit implementation assignment.
metadata:
  internal: true
---

# One roadmap item

Read AGENTS.md, the item in ROADMAP.md, docs/SPEC.md, docs/ARCHITECTURE.md and docs/MODULES.md. A scaffold or documentation request does not authorise roadmap implementation. Confirm every dependency is `done` on current main and the item has no active owner or open PR. If you are already in an assigned worktree, use it; do not launch another worker.

Resolve the Orca executable: `ORCA_CLI_COMMAND` if set; otherwise `orca-dev` when `ORCA_DEV_REPO_ROOT` is set; otherwise `orca-ide` on unmanaged Linux; otherwise `orca`. Run `<orca> skills get orca-cli` on that executable and follow the live guide, not remembered syntax. Never run the Linux screen-reader binary that is also called `orca`. For a new independent lane, use the live guide's worktree create flow with the repo's default base and no parent; base on another feature branch only for assigned stacked work. Setup runs `scripts/worktree-setup.sh`; if it was skipped, run it with explicit root and lane paths. Never copy `.env`, data directories or credentials from another lane.

Branch `feat/<id-lower>-<slug>`, e.g. `feat/c1.2-deploy-api` (rename Orca's branch if needed). Set only your item's Status to `in-progress` and Owner to your branch. Stay inside the item's Scope and the files its Parallel line gives you. Respect the boundaries in docs/MODULES.md. Agree API contract, migration and `packages/ui` changes with the coordinator before dependent code, since they are serialization gates; hand lockfile and migration conflicts to the wave's designated owner. Report unresolved product decisions to the coordinator or user instead of choosing silently, and continue with independent work.

Load the skills the item lists, and also:
- Go server or client code: [wardlock-go-service](../wardlock-go-service/SKILL.md).
- Anything under `internal/edge` or `internal/access`, cookies, tokens or sessions: [wardlock-access](../wardlock-access/SKILL.md), including its test matrix.
- Dashboard or `packages/ui`: [wardlock-web](../wardlock-web/SKILL.md) and [vercel-react-best-practices](../vercel-react-best-practices/SKILL.md) (skip its Next.js and server-rendering rules). Do the skill's Mobbin design research before designing any new or changed screen.

Run targeted tests, then `pnpm check` and `git diff --check`. API or query changes need `pnpm codegen` with the output committed. UI changes need `pnpm browser:test` plus your own look at desktop and mobile widths in light and dark, and Mobbin design references in the PR body. Never put real tokens, passwords, emails or customer content in fixtures, logs, screenshots or PR text.

Set Status to `review`, commit `<ID>: <result>`, push the branch once and open a PR against main titled `<ID>: <title>` using `gh pr create --body-file <temporary file>` with real newlines. The body covers scope, checks run with results, open decisions and any visual gate. Leave the item's PR field empty: the coordinator records the PR in its mark-done commit. Every push to an open PR restarts CI and cancels the running jobs, and each cancelled or failed run notifies the maintainer, so commit everything before pushing, batch review fixes into one push, and rerun a failed job at most once before asking. Never mark `done`; the coordinator does that after merge. Merge only with explicit authority.

For supervised dispatch, follow the injected Orca worker contract for asks, heartbeats and `worker_done`; load the live orchestration worker-contract reference if unclear, and report the PR and changed files exactly once. For a direct assignment, return the PR URL and any held gate. Do not start the next item.

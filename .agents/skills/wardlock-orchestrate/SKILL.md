---
name: wardlock-orchestrate
description: Coordinate explicitly assigned parallel Wardlock roadmap items in Orca worktrees, review their PRs, resolve dependencies and merge when authorised.
---

# Roadmap coordination

This skill runs an assigned coordination task; it is not permission to start product work from a scaffold or documentation request.

Resolve Orca as in [wardlock-item](../wardlock-item/SKILL.md) and load the executable's `skills get orca-cli` and `skills get orchestration` guides, plus placement and worker-contract references when needed. Follow current command syntax; do not copy worker flags from old briefs. Distinguish a full handoff (transfer and stop) from supervised work (task DAG, workers, monitoring) and use only the relevant flow.

Read up-to-date main without overwriting uncommitted work. Select only assigned `todo` items whose dependencies are all `done` on main. ROADMAP's Waves section suggests batches; confirm actual file overlap from each item's Parallel line before launching. At most four workers unless the user sets another limit.

Serialization gates: one lane at a time may change each of these, and dependants start only after the change merges (or the contract is agreed in writing in both briefs):
- migrations and the sqlc schema (`apps/server/migrations/`, `internal/store/queries/`): one migration owner per wave;
- the API contract (`packages/api-spec/openapi.yaml`) and its generated code;
- `packages/ui`;
- cookie and session primitives in `internal/access`;
- lockfiles: `pnpm-lock.yaml` and each module's `go.sum`: one lockfile owner per wave, who adds every dependency the wave needs.

Track claims in the coordinator run so two branches never claim one item. Announce the batch, owners of each gate and the merge order before starting.

Each task gets its own worktree from main and a brief with: item ID, exact scope, dependencies, owned files, gates it may or may not touch, contracts agreed, acceptance tests, skills to load (always `wardlock-item`; `wardlock-go-service` for Go; `wardlock-access` for edge/access; `wardlock-web` for UI), branch name and visual gates (for UI lanes: Mobbin research per wardlock-web, plus desktop/mobile and light/dark checks). The setup hook installs and checks without copying secrets or starting services; each lane has its own ports and SQLite data under `.local/`. Use the live guide's supervised run/task/worker commands and monitoring loop. Never resend an accepted prompt or start a duplicate worker when receipt is uncertain; check first.

Review each PR against its acceptance criteria, boundaries (docs/MODULES.md), owner scoping, the `wardlock-access` test matrix where relevant, fixture privacy and committed codegen. For UI PRs, check the Mobbin design references are present and actually reflected in the result; a missing reference list is a held gate, not a pass. Use a read-only review worker if the assignment includes parallel review. Send blocking findings to the original lane; after two unsuccessful fix rounds, stop and report. Confirm CI passes on the current head. Resolve rebase conflicts in the lane: combine ROADMAP edits per item, keep others' status fields, regenerate lockfiles and generated code rather than splicing them, rerun checks. Merge migrations in version order.

Merge only when the run authorises merging, review is clean, CI passes and visual/product gates are satisfied; otherwise leave the PR in `review` and say why. After an authorised merge, mark the item `done` in a small coordinator commit, then recompute readiness from main. A branch marking itself `done` never satisfies another item's dependency.

Finish with one digest: outcome per item, PR links, checks, held decisions. Settle, release or keep each worker as the live guide describes; keep worktrees with unresolved work. Do not start unassigned items.

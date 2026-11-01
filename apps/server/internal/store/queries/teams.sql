-- SPDX-FileCopyrightText: 2026 CubieCode
-- SPDX-License-Identifier: AGPL-3.0-only
-- (Licence header last: sqlc copies comments above a query into Go docs.)
-- Teams (T7.1). internal/store/teams.go authorises every owner-facing call
-- with Actor.Require before it reaches these.

-- A team's account row: kind 'team', a reserved address nobody can receive
-- mail at, and accepted_at left NULL.
-- name: CreateTeamAccount :exec
INSERT INTO owners (id, email, kind, created_at)
VALUES (sqlc.arg(id), sqlc.arg(email), 'team', sqlc.arg(created_at));

-- name: CreateTeam :exec
INSERT INTO teams (id, name, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.narg(created_by), sqlc.arg(created_at));

-- name: GetTeam :one
SELECT teams.id, teams.name, teams.created_by, teams.created_at, owners.suspended_at
FROM teams JOIN owners ON owners.id = teams.id
WHERE teams.id = sqlc.arg(id);

-- The teams an account can see: an owner's memberships, or a team token's own
-- team, whose account is the team.
-- name: ListTeams :many
SELECT teams.id, teams.name, teams.created_by, teams.created_at, owners.suspended_at
FROM teams JOIN owners ON owners.id = teams.id
WHERE teams.id = sqlc.arg(account_id)
  OR teams.id IN (SELECT team_members.team_id FROM team_members WHERE team_members.owner_id = sqlc.arg(account_id))
ORDER BY teams.name, teams.id;

-- Instance-wide, for `wardlockd admin list-teams` only.
-- name: ListAllTeams :many
SELECT teams.id, teams.name, teams.created_by, teams.created_at, owners.suspended_at
FROM teams JOIN owners ON owners.id = teams.id
WHERE teams.id > sqlc.arg(after_id)
ORDER BY teams.id
LIMIT sqlc.arg(row_limit);

-- Suspension, for `wardlockd admin` only: the same column as an owner's, so
-- every join that keeps a suspended owner's sites, tokens and evidence keeps a
-- suspended team's. Suspending keeps the first time; reinstating clears it.
-- name: SuspendTeam :execrows
UPDATE owners SET suspended_at = COALESCE(suspended_at, sqlc.arg(suspended_at)) WHERE id = sqlc.arg(id) AND kind = 'team';

-- name: ReinstateTeam :execrows
UPDATE owners SET suspended_at = NULL WHERE id = sqlc.arg(id) AND kind = 'team';

-- name: GetAccountSuspendedAt :one
SELECT suspended_at FROM owners WHERE id = sqlc.arg(id);

-- name: RenameTeam :execrows
UPDATE teams SET name = sqlc.arg(name) WHERE id = sqlc.arg(id);

-- Deleting the account takes the team, its members, invitations, tokens,
-- groups, tags and blob holdings with it. A team with a site cannot be
-- deleted: sites.owner_id has no cascade.
-- name: DeleteTeamAccount :execrows
DELETE FROM owners WHERE id = sqlc.arg(id) AND kind = 'team';

-- name: CountTeamsCreatedBy :one
SELECT COUNT(*) FROM teams WHERE created_by = sqlc.arg(owner_id);

-- name: GetOwnerMaxTeams :one
SELECT max_teams FROM owners WHERE id = sqlc.arg(id) AND kind = 'owner';

-- name: SetOwnerMaxTeams :execrows
UPDATE owners SET max_teams = sqlc.narg(max_teams) WHERE id = sqlc.arg(id) AND kind = 'owner';

-- What an Actor carries: read on every authenticated request.
-- name: ListMemberships :many
SELECT team_id, role FROM team_members WHERE owner_id = sqlc.arg(owner_id) ORDER BY team_id;

-- name: AddTeamMember :exec
INSERT INTO team_members (team_id, owner_id, role, created_at)
VALUES (sqlc.arg(team_id), sqlc.arg(owner_id), sqlc.arg(role), sqlc.arg(created_at))
ON CONFLICT (team_id, owner_id) DO NOTHING;

-- Suspended members stay listed: suspension is the operator's, membership
-- the team's. Ordered by address after the cursor.
-- name: ListTeamMembers :many
SELECT team_members.team_id, team_members.owner_id, team_members.role, team_members.created_at, owners.email, owners.suspended_at
FROM team_members JOIN owners ON owners.id = team_members.owner_id
WHERE team_members.team_id = sqlc.arg(team_id) AND owners.email > sqlc.arg(after_email)
ORDER BY owners.email
LIMIT sqlc.arg(row_limit);

-- name: GetTeamMemberRole :one
SELECT role FROM team_members WHERE team_id = sqlc.arg(team_id) AND owner_id = sqlc.arg(owner_id);

-- Admins other than owner_id who can still act: a suspended admin keeps a
-- team running for nobody.
-- name: CountOtherWorkingAdmins :one
SELECT COUNT(*) FROM team_members
JOIN owners ON owners.id = team_members.owner_id
WHERE team_members.team_id = sqlc.arg(team_id) AND team_members.role = 'admin'
  AND team_members.owner_id <> sqlc.arg(owner_id) AND owners.suspended_at IS NULL;

-- name: SetTeamMemberRole :execrows
UPDATE team_members SET role = sqlc.arg(role) WHERE team_id = sqlc.arg(team_id) AND owner_id = sqlc.arg(owner_id);

-- name: RemoveTeamMember :execrows
DELETE FROM team_members WHERE team_id = sqlc.arg(team_id) AND owner_id = sqlc.arg(owner_id);

-- Who hears about a team's sites: its admins who can read mail, while the
-- team is not suspended.
-- name: ListTeamAdminIDs :many
SELECT team_members.owner_id FROM team_members
JOIN owners ON owners.id = team_members.owner_id
JOIN owners AS team_account ON team_account.id = team_members.team_id
WHERE team_members.team_id = sqlc.arg(team_id) AND team_members.role = 'admin'
  AND owners.suspended_at IS NULL AND team_account.suspended_at IS NULL
ORDER BY team_members.owner_id;

-- 1 while owner_id is a working admin: an admin of the team, not suspended, in
-- a team that is not suspended.
-- name: CountWorkingAdmin :one
SELECT COUNT(*) FROM team_members
JOIN owners ON owners.id = team_members.owner_id
JOIN owners AS team_account ON team_account.id = team_members.team_id
WHERE team_members.team_id = sqlc.arg(team_id) AND team_members.owner_id = sqlc.arg(owner_id) AND team_members.role = 'admin'
  AND owners.suspended_at IS NULL AND team_account.suspended_at IS NULL;

-- name: CountMembersWithEmail :one
SELECT COUNT(*) FROM team_members
JOIN owners ON owners.id = team_members.owner_id
WHERE team_members.team_id = sqlc.arg(team_id) AND owners.email = sqlc.arg(email);

-- Every invitation to an address, spent or not: a member who goes needs
-- inviting afresh.
-- name: DeleteInvitationsTo :exec
DELETE FROM team_invitations WHERE team_id = sqlc.arg(team_id) AND email = sqlc.arg(email);

-- Open invitations someone sent, when they stop being an admin: what an admin
-- set up must not outlive their authority.
-- name: DeleteOpenInvitationsFrom :exec
DELETE FROM team_invitations
WHERE team_id = sqlc.arg(team_id) AND invited_by = sqlc.narg(invited_by) AND accepted_at IS NULL;

-- name: GetAccountKind :one
SELECT kind FROM owners WHERE id = sqlc.arg(id);

-- Inviting an address again replaces its invitation, so a fresh one can
-- follow an expired or accepted one.
-- name: UpsertTeamInvitation :exec
INSERT INTO team_invitations (id, team_id, email, role, invited_by, created_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(team_id), sqlc.arg(email), sqlc.arg(role), sqlc.narg(invited_by), sqlc.arg(created_at), sqlc.arg(expires_at))
ON CONFLICT (team_id, email) DO UPDATE SET
  id = excluded.id, role = excluded.role, invited_by = excluded.invited_by,
  created_at = excluded.created_at, expires_at = excluded.expires_at, accepted_at = NULL;

-- name: GetTeamInvitation :one
SELECT team_invitations.id, team_invitations.team_id, team_invitations.email, team_invitations.role, team_invitations.invited_by,
  team_invitations.created_at, team_invitations.expires_at, team_invitations.accepted_at, teams.name AS team_name
FROM team_invitations JOIN teams ON teams.id = team_invitations.team_id
WHERE team_invitations.id = sqlc.arg(id);

-- Open invitations of a team, newest first.
-- name: ListTeamInvitations :many
SELECT team_invitations.id, team_invitations.team_id, team_invitations.email, team_invitations.role, team_invitations.invited_by,
  team_invitations.created_at, team_invitations.expires_at, team_invitations.accepted_at, teams.name AS team_name
FROM team_invitations JOIN teams ON teams.id = team_invitations.team_id
WHERE team_invitations.team_id = sqlc.arg(team_id) AND team_invitations.accepted_at IS NULL AND team_invitations.expires_at > sqlc.arg(now)
ORDER BY team_invitations.created_at DESC, team_invitations.id DESC;

-- Open invitations to one address, newest first. One whose inviter is no
-- longer a working admin is dead, as Accept finds it.
-- name: ListInvitationsForEmail :many
SELECT team_invitations.id, team_invitations.team_id, team_invitations.email, team_invitations.role, team_invitations.invited_by,
  team_invitations.created_at, team_invitations.expires_at, team_invitations.accepted_at, teams.name AS team_name
FROM team_invitations JOIN teams ON teams.id = team_invitations.team_id
WHERE team_invitations.email = sqlc.arg(email) AND team_invitations.accepted_at IS NULL AND team_invitations.expires_at > sqlc.arg(now)
  AND EXISTS (
    SELECT 1 FROM team_members
    JOIN owners AS inviter ON inviter.id = team_members.owner_id
    JOIN owners AS team_account ON team_account.id = team_members.team_id
    WHERE team_members.team_id = team_invitations.team_id AND team_members.owner_id = team_invitations.invited_by
      AND team_members.role = 'admin' AND inviter.suspended_at IS NULL AND team_account.suspended_at IS NULL
  )
ORDER BY team_invitations.created_at DESC, team_invitations.id DESC;

-- name: MarkTeamInvitationAccepted :exec
UPDATE team_invitations SET accepted_at = sqlc.arg(accepted_at) WHERE id = sqlc.arg(id);

-- name: DeleteTeamInvitation :execrows
DELETE FROM team_invitations WHERE id = sqlc.arg(id) AND team_id = sqlc.arg(team_id);

-- Moves a site to another account (T7.1). The source is named so a site
-- moved meanwhile changes nothing, and an unavailable site cannot move. Its
-- group belongs to the source, so it goes.
-- name: SetSiteOwner :execrows
UPDATE sites SET owner_id = sqlc.arg(to_owner), group_id = NULL
WHERE sites.id = sqlc.arg(id) AND sites.owner_id = sqlc.arg(from_owner) AND sites.taken_down_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM owners WHERE owners.id = sites.owner_id AND owners.suspended_at IS NOT NULL);

-- Gives the new account what the old one held of the site's files, so it can
-- download them and activate deploys. Only what the source held: a transfer
-- never turns a listed hash into a held one.
-- name: CopySiteBlobHoldings :exec
INSERT INTO owner_blobs (owner_id, sha256, created_at)
SELECT DISTINCT CAST(sqlc.arg(to_owner) AS TEXT), deploy_files.sha256, CAST(sqlc.arg(created_at) AS INTEGER)
FROM deploy_files
JOIN deploys ON deploys.id = deploy_files.deploy_id
JOIN owner_blobs AS held ON held.owner_id = sqlc.arg(from_owner) AND held.sha256 = deploy_files.sha256
WHERE deploys.site_id = sqlc.arg(site_id)
ON CONFLICT (owner_id, sha256) DO NOTHING;

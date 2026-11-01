// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  Dialog,
  Notice,
  StatusBadge,
  Table,
  type TableColumn,
} from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import { formatDate } from '../tokens/format.js';
import { roleNames, roles, teamRefusal } from './messages.js';
import { useScope, type Team, type TeamRole } from './scope.js';

type Member = components['schemas']['TeamMember'];

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; members: Member[]; nextCursor: string | undefined };

type Asking =
  | { kind: 'remove'; member: Member }
  | { kind: 'promote'; member: Member }
  | { kind: 'step-down'; member: Member; role: TeamRole };

/**
 * The team's members. Everyone sees who is in the team and with what role;
 * admins change roles and remove people. A new role takes effect on Save, so
 * arrowing through the list changes nothing. Making someone an admin, stepping
 * down and removing someone are each confirmed first.
 */
export function MembersCard({
  api,
  team,
  admin,
}: {
  api: ApiClient;
  team: Team;
  admin: boolean;
}) {
  const { ownerId, refresh } = useScope();
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  const [problem, setProblem] = useState<string | null>(null);
  const [asking, setAsking] = useState<Asking | null>(null);
  const path = { teamId: team.id };
  // Bumped by every change, so an older page cannot undo it.
  const generation = useRef(0);

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/teams/{teamId}/members', {
        params: { path: { teamId: team.id } },
      })
      .then(
        ({ data }) =>
          live &&
          setState(
            data
              ? {
                  kind: 'ready',
                  members: data.items,
                  nextCursor: data.nextCursor,
                }
              : { kind: 'error' },
          ),
        () => live && setState({ kind: 'error' }),
      );
    return () => {
      live = false;
    };
  }, [api, team.id, attempt]);

  const update = (change: (members: Member[]) => Member[]) => {
    generation.current += 1;
    setState((current) =>
      current.kind === 'ready'
        ? { ...current, members: change(current.members) }
        : current,
    );
  };

  const loadMore = async () => {
    if (state.kind !== 'ready' || !state.nextCursor) return;
    const started = generation.current;
    setMore('loading');
    const { data } = await api
      .GET('/api/v0/teams/{teamId}/members', {
        params: { path, query: { cursor: state.nextCursor } },
      })
      .catch(() => ({ data: undefined }));
    if (started !== generation.current) return;
    if (!data) return setMore('error');
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? {
            kind: 'ready',
            members: [
              ...current.members,
              ...data.items.filter(
                (m) => !current.members.some((c) => c.ownerId === m.ownerId),
              ),
            ],
            nextCursor: data.nextCursor,
          }
        : current,
    );
  };

  /** Answers null, or why it was refused for whoever asked to show. */
  const setRole = async (member: Member, role: TeamRole) => {
    const result = await api
      .PATCH('/api/v0/teams/{teamId}/members/{ownerId}', {
        params: { path: { ...path, ownerId: member.ownerId } },
        body: { role },
      })
      .catch(() => null);
    if (!result?.data) return teamRefusal(result);
    const changed = result.data;
    update((members) =>
      members.map((m) => (m.ownerId === changed.ownerId ? changed : m)),
    );
    if (member.ownerId === ownerId) await refresh();
    return null;
  };

  const remove = async (member: Member) => {
    setProblem(null);
    const result = await api
      .DELETE('/api/v0/teams/{teamId}/members/{ownerId}', {
        params: { path: { ...path, ownerId: member.ownerId } },
      })
      .catch(() => null);
    if (result?.response.ok || result?.response.status === 404) {
      update((members) => members.filter((m) => m.ownerId !== member.ownerId));
      return null;
    }
    return teamRefusal(result);
  };

  const columns: TableColumn<Member>[] = [
    {
      header: 'Member',
      cell: (member) => (
        <span className="team-member">
          <span className="team-email">{member.email}</span>
          {member.ownerId === ownerId ? (
            <span className="team-muted">You</span>
          ) : null}
          {member.suspended ? (
            <StatusBadge tone="warning">Suspended</StatusBadge>
          ) : null}
        </span>
      ),
    },
    {
      header: 'Role',
      nowrap: true,
      cell: (member) =>
        admin ? (
          <RoleSelect
            key={member.role}
            member={member}
            onSave={(role) => {
              setProblem(null);
              if (role === 'admin') setAsking({ kind: 'promote', member });
              else if (member.ownerId === ownerId)
                setAsking({ kind: 'step-down', member, role });
              else void setRole(member, role).then(setProblem);
            }}
          />
        ) : (
          roleNames[member.role]
        ),
    },
    {
      header: 'Joined',
      nowrap: true,
      cell: (member) => formatDate(member.createdAt),
    },
    ...(admin
      ? [
          {
            header: 'Actions',
            cell: (member: Member) =>
              member.ownerId === ownerId ? null : (
                <Button
                  type="button"
                  aria-label={`Remove ${member.email}`}
                  onClick={() => {
                    setProblem(null);
                    setAsking({ kind: 'remove', member });
                  }}
                >
                  Remove
                </Button>
              ),
          },
        ]
      : []),
  ];

  return (
    <Card title="Members">
      {state.kind === 'loading' ? <p role="status">Loading members…</p> : null}
      {state.kind === 'error' ? (
        <div className="team-stack">
          <Notice tone="danger">
            Could not load the members. Check your connection, then try again.
          </Notice>
          <div>
            <Button
              type="button"
              onClick={() => {
                setState({ kind: 'loading' });
                setAttempt((n) => n + 1);
              }}
            >
              Try again
            </Button>
          </div>
        </div>
      ) : null}
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
      {state.kind === 'ready' ? (
        <Table
          caption="Team members"
          columns={columns}
          rows={state.members}
          rowKey={(member) => member.ownerId}
        />
      ) : null}
      {more === 'error' ? (
        <Notice tone="danger">Could not load more members. Try again.</Notice>
      ) : null}
      {state.kind === 'ready' && state.nextCursor ? (
        <div>
          <Button
            type="button"
            disabled={more === 'loading'}
            onClick={() => void loadMore()}
          >
            {more === 'loading' ? 'Loading…' : 'Show more members'}
          </Button>
        </div>
      ) : null}
      {asking?.kind === 'remove' ? (
        <ConfirmDialog
          title={`Remove ${asking.member.email} from ${team.name}?`}
          action="Remove member"
          busyLabel="Removing…"
          onClose={() => setAsking(null)}
          onConfirm={() => remove(asking.member)}
        >
          <p>
            They lose access to the team’s sites at once, and any open
            invitations they sent are withdrawn. What they made stays with the
            team.
          </p>
        </ConfirmDialog>
      ) : null}
      {asking?.kind === 'promote' ? (
        <ConfirmDialog
          title={`Make ${asking.member.email} an admin of ${team.name}?`}
          action="Make admin"
          busyLabel="Changing…"
          onClose={() => setAsking(null)}
          onConfirm={() => setRole(asking.member, 'admin')}
        >
          <p>
            They can manage members, tokens and the team itself, and delete the
            team once it has no sites.
          </p>
        </ConfirmDialog>
      ) : null}
      {asking?.kind === 'step-down' ? (
        <ConfirmDialog
          title={`Stop being an admin of ${team.name}?`}
          action={`Become ${asking.role === 'editor' ? 'an editor' : 'a viewer'}`}
          busyLabel="Changing…"
          onClose={() => setAsking(null)}
          onConfirm={() => setRole(asking.member, asking.role)}
        >
          <p>
            You lose members, tokens and team settings straight away, and any
            open invitations you sent are withdrawn. Only another admin can make
            you an admin again.
          </p>
        </ConfirmDialog>
      ) : null}
    </Card>
  );
}

/**
 * A member's role with Save beside it once another is chosen. Keyed on the
 * saved role, so a saved change starts it afresh.
 */
function RoleSelect({
  member,
  onSave,
}: {
  member: Member;
  onSave: (role: TeamRole) => void;
}) {
  const [role, setRole] = useState(member.role);
  return (
    <span className="team-role-row">
      <select
        className="wl-input team-role"
        aria-label={`Role of ${member.email}`}
        value={role}
        onChange={(event) => setRole(event.target.value as TeamRole)}
      >
        {roles.map((r) => (
          <option key={r.role} value={r.role}>
            {roleNames[r.role]}
          </option>
        ))}
      </select>
      {role === member.role ? null : (
        <Button
          type="button"
          aria-label={`Save the role of ${member.email}`}
          onClick={() => onSave(role)}
        >
          Save
        </Button>
      )}
    </span>
  );
}

/** A confirmation whose action answers null, or the refusal to show. */
export function ConfirmDialog({
  title,
  action,
  busyLabel,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  action: string;
  busyLabel: string;
  onConfirm: () => Promise<string | null>;
  onClose: () => void;
  children: React.ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  return (
    <Dialog
      open
      title={title}
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="danger"
            disabled={busy}
            onClick={() => {
              setBusy(true);
              setProblem(null);
              void onConfirm().then((refusal) => {
                setBusy(false);
                if (refusal === null) onClose();
                else setProblem(refusal);
              });
            }}
          >
            {busy ? busyLabel : action}
          </Button>
        </>
      }
    >
      {children}
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
    </Dialog>
  );
}

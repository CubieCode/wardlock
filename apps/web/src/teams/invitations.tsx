// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  Notice,
  SelectField,
  Table,
  TextField,
  type TableColumn,
} from '@wardlock/ui';
import { useEffect, useState, type FormEvent } from 'react';
import { formatDate } from '../tokens/format.js';
import { roleNames, roles, teamRefusal } from './messages.js';
import type { TeamRole } from './scope.js';

type Invitation = components['schemas']['TeamInvitation'];

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; invitations: Invitation[] };

/**
 * For admins: invite an address with a role, and the invitations still open.
 * Inviting an address again replaces its invitation.
 */
export function InvitationsCard({
  api,
  teamId,
}: {
  api: ApiClient;
  teamId: string;
}) {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [said, setSaid] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const path = { teamId };

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/teams/{teamId}/invitations', {
        params: { path: { teamId } },
      })
      .then(
        ({ data }) =>
          live &&
          setState(
            data
              ? { kind: 'ready', invitations: data.items }
              : { kind: 'error' },
          ),
        () => live && setState({ kind: 'error' }),
      );
    return () => {
      live = false;
    };
  }, [api, teamId, attempt]);

  const change = (update: (items: Invitation[]) => Invitation[]) =>
    setState((current) =>
      current.kind === 'ready'
        ? { kind: 'ready', invitations: update(current.invitations) }
        : current,
    );

  const cancel = async (invitation: Invitation) => {
    setSaid(null);
    setProblem(null);
    const result = await api
      .DELETE('/api/v0/teams/{teamId}/invitations/{invitationId}', {
        params: { path: { ...path, invitationId: invitation.id } },
      })
      .catch(() => null);
    // 404: it had already gone, which is what was asked for.
    if (result?.response.ok || result?.response.status === 404) {
      change((items) => items.filter((i) => i.id !== invitation.id));
      setSaid(`Cancelled the invitation to ${invitation.email}.`);
    } else setProblem(teamRefusal(result));
  };

  const columns: TableColumn<Invitation>[] = [
    {
      header: 'Address',
      cell: (invitation) => (
        <span className="team-email">{invitation.email}</span>
      ),
    },
    {
      header: 'Role',
      nowrap: true,
      cell: (invitation) => roleNames[invitation.role],
    },
    {
      header: 'Expires',
      nowrap: true,
      cell: (invitation) => formatDate(invitation.expiresAt),
    },
    {
      header: 'Actions',
      cell: (invitation) => (
        <Button
          type="button"
          aria-label={`Cancel the invitation to ${invitation.email}`}
          onClick={() => void cancel(invitation)}
        >
          Cancel
        </Button>
      ),
    },
  ];

  return (
    <Card title="Invitations">
      <InviteForm
        api={api}
        teamId={teamId}
        onInvited={(invitation) => {
          setProblem(null);
          change((items) => [
            invitation,
            ...items.filter((i) => i.email !== invitation.email),
          ]);
          setSaid(
            `Invited ${invitation.email} as ${roleNames[invitation.role].toLowerCase()}. The invitation lasts 7 days.`,
          );
        }}
      />
      <p className={said ? 'team-said' : 'wl-visually-hidden'} role="status">
        {said}
      </p>
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
      {state.kind === 'loading' ? (
        <p role="status">Loading invitations…</p>
      ) : null}
      {state.kind === 'error' ? (
        <div className="team-stack">
          <Notice tone="danger">
            Could not load the invitations. Check your connection, then try
            again.
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
      {state.kind === 'ready' && state.invitations.length === 0 ? (
        <p className="team-muted">No open invitations.</p>
      ) : null}
      {state.kind === 'ready' && state.invitations.length > 0 ? (
        <Table
          caption="Open invitations"
          columns={columns}
          rows={state.invitations}
          rowKey={(invitation) => invitation.id}
        />
      ) : null}
    </Card>
  );
}

function InviteForm({
  api,
  teamId,
  onInvited,
}: {
  api: ApiClient;
  teamId: string;
  onInvited: (invitation: Invitation) => void;
}) {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<TeamRole>('editor');
  const [error, setError] = useState<string>();
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const invite = async (event: FormEvent) => {
    event.preventDefault();
    const address = email.trim();
    setProblem(null);
    if (!address) return setError('Enter the address to invite.');
    setError(undefined);
    setBusy(true);
    const result = await api
      .POST('/api/v0/teams/{teamId}/invitations', {
        params: { path: { teamId } },
        body: { email: address, role },
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) {
      setEmail('');
      return onInvited(result.data);
    }
    const onField =
      result?.error?.target === 'email' ||
      result?.error?.code === 'already_member';
    if (onField) setError(teamRefusal(result));
    else setProblem(teamRefusal(result));
  };

  return (
    <form className="team-invite" onSubmit={invite} noValidate>
      <TextField
        label="Email address"
        type="email"
        autoComplete="off"
        value={email}
        error={error}
        onChange={(event) => setEmail(event.target.value)}
      />
      <SelectField
        label="Role"
        value={role}
        onChange={(event) => setRole(event.target.value as TeamRole)}
      >
        {roles.map((r) => (
          <option key={r.role} value={r.role}>
            {roleNames[r.role]}
          </option>
        ))}
      </SelectField>
      <div className="team-invite-action">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? 'Inviting…' : 'Send invitation'}
        </Button>
      </div>
      <p className="team-muted team-invite-hint">
        {roleNames[role]}: {roles.find((r) => r.role === role)?.hint}.
      </p>
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
    </form>
  );
}

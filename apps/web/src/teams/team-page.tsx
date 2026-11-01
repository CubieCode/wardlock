// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, Notice, PageHeader, TextField } from '@wardlock/ui';
import { useEffect, useState, type FormEvent } from 'react';
import { useTitle } from '../shell/page.js';
import { DangerCards } from './danger.js';
import { InvitationsCard } from './invitations.js';
import { MembersCard } from './members.js';
import { roleNames, teamRefusal } from './messages.js';
import { useScope, type Team } from './scope.js';
import { TeamTokensCard } from './team-tokens.js';
import './teams.css';

type State =
  | { kind: 'loading' }
  | { kind: 'missing' }
  | { kind: 'error' }
  | { kind: 'ready'; team: Team };

/**
 * `/teams/:teamId`: the team's name, members, invitations and tokens, and
 * leaving or deleting it. Everyone in the team sees the members and can
 * leave; everything else here is for its admins and left out for the rest.
 */
export function TeamPage({ api, teamId }: { api: ApiClient; teamId: string }) {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const { teams } = useScope();
  useTitle(state.kind === 'ready' ? state.team.name : 'Team');

  useEffect(() => {
    let live = true;
    api.GET('/api/v0/teams/{teamId}', { params: { path: { teamId } } }).then(
      ({ data, response }) => {
        if (!live) return;
        if (data) setState({ kind: 'ready', team: data });
        else setState({ kind: response.status === 404 ? 'missing' : 'error' });
      },
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, teamId, attempt]);

  if (state.kind === 'loading') return <p role="status">Loading team…</p>;
  if (state.kind === 'missing') return <TeamMissing />;
  if (state.kind === 'error')
    return (
      <>
        <PageHeader title="Team" />
        <div className="team-stack">
          <Notice tone="danger">
            Could not load this team. Check your connection, then try again.
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
      </>
    );

  // The role as last read for the switcher, so a change to it here (or a
  // refused call) takes the controls away without a reload.
  const known =
    teams.kind === 'ready'
      ? teams.teams.find((t) => t.id === teamId)
      : undefined;
  const team = { ...state.team, role: known?.role ?? state.team.role };
  const admin = team.role === 'admin';
  return (
    <>
      <PageHeader
        title={team.name}
        description={`You are ${team.role === 'admin' ? 'an' : 'a'} ${roleNames[team.role].toLowerCase()} of this team.`}
      />
      <div className="team-stack">
        {admin ? (
          <NameCard
            api={api}
            team={team}
            onRenamed={(name) =>
              setState({ kind: 'ready', team: { ...state.team, name } })
            }
          />
        ) : null}
        <MembersCard api={api} team={team} admin={admin} />
        {admin ? <InvitationsCard api={api} teamId={team.id} /> : null}
        {admin ? <TeamTokensCard api={api} team={team} /> : null}
        <DangerCards api={api} team={team} admin={admin} />
      </div>
    </>
  );
}

/** A team this owner is not in looks the same as one that does not exist. */
export function TeamMissing() {
  useTitle('Team not found');
  return (
    <>
      <PageHeader
        title="Team not found"
        description="There is no such team, or you are not a member of it."
      />
      <p>
        <a href="/">Go to your overview</a>
      </p>
    </>
  );
}

const maxName = 80;

function NameCard({
  api,
  team,
  onRenamed,
}: {
  api: ApiClient;
  team: Team;
  onRenamed: (name: string) => void;
}) {
  const { refresh } = useScope();
  const [name, setName] = useState(team.name);
  const [error, setError] = useState<string>();
  const [said, setSaid] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    const trimmed = name.trim();
    setSaid(null);
    if (!trimmed) return setError('Enter a name for the team.');
    setError(undefined);
    setBusy(true);
    const result = await api
      .PATCH('/api/v0/teams/{teamId}', {
        params: { path: { teamId: team.id } },
        body: { name: trimmed },
      })
      .catch(() => null);
    setBusy(false);
    if (!result?.data) return setError(teamRefusal(result));
    onRenamed(result.data.name);
    setSaid('Saved the team’s name.');
    void refresh();
  };

  return (
    <Card title="Team name">
      <form className="team-form" onSubmit={save} noValidate>
        <TextField
          label="Name"
          hint="Everyone in the team sees it. Names need not be unique."
          maxLength={maxName}
          autoComplete="off"
          value={name}
          error={error}
          onChange={(event) => setName(event.target.value)}
        />
        <div>
          <Button type="submit" disabled={busy}>
            {busy ? 'Saving…' : 'Save name'}
          </Button>
        </div>
      </form>
      <p className={said ? 'team-said' : 'wl-visually-hidden'} role="status">
        {said}
      </p>
    </Card>
  );
}

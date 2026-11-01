// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { Button, Card, EmptyState, Notice, PageHeader } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { useTitle } from '../shell/page.js';
import { navigate } from '../shell/router.js';
import { formatDate } from '../tokens/format.js';
import { roleNames, teamRefusal } from './messages.js';
import { useScope } from './scope.js';
import './teams.css';

type Invitation = components['schemas']['TeamInvitation'];

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; invitations: Invitation[] };

/**
 * `/team-invitations`, where the invitation email leads: the invitations to
 * the signed-in owner's own address, each to accept or decline. The email
 * holds no secret, so signing in is what proves the address is theirs.
 */
export function TeamInvitationsPage({ api }: { api: ApiClient }) {
  useTitle('Team invitations');
  const { refresh } = useScope();
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api.GET('/api/v0/team-invitations').then(
      ({ data }) =>
        live &&
        setState(
          data ? { kind: 'ready', invitations: data.items } : { kind: 'error' },
        ),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  const reload = () => setAttempt((n) => n + 1);

  const accept = async (invitation: Invitation) => {
    setBusy(invitation.id);
    setProblem(null);
    const result = await api
      .POST('/api/v0/team-invitations/{invitationId}/accept', {
        params: { path: { invitationId: invitation.id } },
      })
      .catch(() => null);
    if (result?.data) {
      await refresh();
      return navigate(`/sites?team=${encodeURIComponent(result.data.id)}`);
    }
    setBusy(null);
    if (result?.response.status === 404) {
      setProblem(
        `The invitation to ${invitation.teamName} is no longer open. Ask an admin of the team to send a new one.`,
      );
      void refresh();
      return reload();
    }
    setProblem(teamRefusal(result));
  };

  const decline = async (invitation: Invitation) => {
    setBusy(invitation.id);
    setProblem(null);
    const result = await api
      .POST('/api/v0/team-invitations/{invitationId}/decline', {
        params: { path: { invitationId: invitation.id } },
      })
      .catch(() => null);
    setBusy(null);
    // 404: it had already gone, which is what was asked for.
    if (result?.response.ok || result?.response.status === 404) {
      setState((current) =>
        current.kind === 'ready'
          ? {
              kind: 'ready',
              invitations: current.invitations.filter(
                (i) => i.id !== invitation.id,
              ),
            }
          : current,
      );
      return void refresh();
    }
    setProblem(teamRefusal(result));
  };

  return (
    <>
      <PageHeader
        title="Team invitations"
        description="Teams that have invited this account. Accepting gives you the role shown."
      />
      <div className="team-stack">
        {problem ? <Notice tone="danger">{problem}</Notice> : null}
        {state.kind === 'loading' ? (
          <p role="status">Loading invitations…</p>
        ) : null}
        {state.kind === 'error' ? (
          <>
            <Notice tone="danger">
              Could not load your invitations. Check your connection, then try
              again.
            </Notice>
            <div>
              <Button
                type="button"
                onClick={() => {
                  setState({ kind: 'loading' });
                  reload();
                }}
              >
                Try again
              </Button>
            </div>
          </>
        ) : null}
        {state.kind === 'ready' && state.invitations.length === 0 ? (
          <EmptyState
            title="No open invitations"
            action={<a href="/">Go to your overview</a>}
          >
            <p>
              An invitation shows here while you are signed in with the address
              it was sent to. Invitations last 7 days.
            </p>
          </EmptyState>
        ) : null}
        {state.kind === 'ready' &&
          state.invitations.map((invitation) => (
            <Card key={invitation.id} title={invitation.teamName}>
              <p>
                You are invited as{' '}
                <strong>{roleNames[invitation.role].toLowerCase()}</strong>. The
                invitation expires on {formatDate(invitation.expiresAt)}.
              </p>
              <div className="team-actions">
                <Button
                  type="button"
                  variant="primary"
                  disabled={busy !== null}
                  aria-label={`Accept the invitation to ${invitation.teamName}`}
                  onClick={() => void accept(invitation)}
                >
                  Accept
                </Button>
                <Button
                  type="button"
                  disabled={busy !== null}
                  aria-label={`Decline the invitation to ${invitation.teamName}`}
                  onClick={() => void decline(invitation)}
                >
                  Decline
                </Button>
              </div>
            </Card>
          ))}
      </div>
    </>
  );
}

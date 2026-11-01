// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, EmptyState, Notice, Table } from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import { CreateTokenDialog } from '../tokens/create-token.js';
import {
  formatDate,
  scopesText,
  type ApiToken,
  type CreatedApiToken,
} from '../tokens/format.js';
import { RevokeTokenDialog } from '../tokens/revoke-token.js';
import { ShownOnceSecretDialog } from '../tokens/shown-once-secret.js';
import { tokenColumns } from '../tokens/tokens-page.js';
import '../tokens/tokens.css';
import { teamRefusal } from './messages.js';
import type { Team } from './scope.js';

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; tokens: ApiToken[]; nextCursor: string | undefined };

type Open =
  | { kind: 'none' }
  | { kind: 'create' }
  | { kind: 'revoke'; token: ApiToken }
  // The one-time response: held only while its dialog is open.
  | { kind: 'reveal'; created: CreatedApiToken };

/**
 * For admins: tokens owned by the team, for CI. They act as an editor limited
 * by their scopes and outlive any one member. Made and revoked like the
 * owner's own tokens, and shown once in the same way.
 */
export function TeamTokensCard({ api, team }: { api: ApiClient; team: Team }) {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [open, setOpen] = useState<Open>({ kind: 'none' });
  const [done, setDone] = useState<string | null>(null);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  // Bumped by every change, so an older page cannot put back a revoked token.
  const generation = useRef(0);
  const teamId = team.id;

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/teams/{teamId}/tokens', { params: { path: { teamId } } })
      .then(
        ({ data }) =>
          live &&
          setState(
            data
              ? {
                  kind: 'ready',
                  tokens: data.items,
                  nextCursor: data.nextCursor,
                }
              : { kind: 'error' },
          ),
        () => live && setState({ kind: 'error' }),
      );
    return () => {
      live = false;
    };
  }, [api, teamId, attempt]);

  const update = (change: (tokens: ApiToken[]) => ApiToken[]) => {
    generation.current += 1;
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? { ...current, tokens: change(current.tokens) }
        : current,
    );
  };

  const loadOlder = async () => {
    if (state.kind !== 'ready' || !state.nextCursor) return;
    const started = generation.current;
    setMore('loading');
    const { data } = await api
      .GET('/api/v0/teams/{teamId}/tokens', {
        params: { path: { teamId }, query: { cursor: state.nextCursor } },
      })
      .catch(() => ({ data: undefined }));
    if (started !== generation.current) return;
    if (!data) return setMore('error');
    setMore('idle');
    setState((current) =>
      current.kind === 'ready'
        ? {
            kind: 'ready',
            tokens: [
              ...current.tokens,
              ...data.items.filter(
                (t) => !current.tokens.some((c) => c.id === t.id),
              ),
            ],
            nextCursor: data.nextCursor,
          }
        : current,
    );
  };

  const close = () => setOpen({ kind: 'none' });
  const columns = tokenColumns((token) => {
    setDone(null);
    setOpen({ kind: 'revoke', token });
  });

  return (
    <Card title="Team tokens">
      <div className="token-intro">
        <p>
          For CI jobs and tools that should keep working when people leave. A
          team token can deploy and change this team’s sites as far as its
          scopes allow, and never manages members.
        </p>
        <Button
          type="button"
          variant="primary"
          disabled={state.kind !== 'ready'}
          onClick={() => {
            setDone(null);
            setOpen({ kind: 'create' });
          }}
        >
          Create team token
        </Button>
      </div>
      {done ? <Notice>{done}</Notice> : null}
      {state.kind === 'loading' ? (
        <p role="status">Loading team tokens…</p>
      ) : null}
      {state.kind === 'error' ? (
        <div className="token-error">
          <Notice tone="danger">
            Could not load the team’s tokens. Check your connection, then try
            again.
          </Notice>
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
      ) : null}
      {state.kind === 'ready' && state.tokens.length === 0 ? (
        <EmptyState title="No team tokens yet">
          <p>Give each job its own, so you can revoke it alone.</p>
        </EmptyState>
      ) : null}
      {state.kind === 'ready' && state.tokens.length > 0 ? (
        <Table
          caption="Team tokens, newest first"
          columns={columns}
          rows={state.tokens}
          rowKey={(token) => token.id}
        />
      ) : null}
      {more === 'error' ? (
        <Notice tone="danger">Could not load more tokens. Try again.</Notice>
      ) : null}
      {state.kind === 'ready' && state.nextCursor ? (
        <div>
          <Button
            type="button"
            disabled={more === 'loading'}
            onClick={() => void loadOlder()}
          >
            {more === 'loading' ? 'Loading…' : 'Show older tokens'}
          </Button>
        </div>
      ) : null}

      {open.kind === 'create' ? (
        <CreateTokenDialog
          api={api}
          title={`Create a token for ${team.name}`}
          scopesHint="Both can read the team’s sites and deploys. Choose only what it needs."
          create={(body) =>
            api.POST('/api/v0/teams/{teamId}/tokens', {
              params: { path: { teamId } },
              body,
            })
          }
          refusal={teamRefusal}
          onClose={close}
          onCreated={(created) => {
            update((tokens) => [created.apiToken, ...tokens]);
            setOpen({ kind: 'reveal', created });
          }}
        />
      ) : null}
      {open.kind === 'revoke' ? (
        <RevokeTokenDialog
          api={api}
          token={open.token}
          revoke={() =>
            api.DELETE('/api/v0/teams/{teamId}/tokens/{tokenId}', {
              params: { path: { teamId, tokenId: open.token.id } },
            })
          }
          onClose={close}
          onRevoked={() => {
            update((tokens) => tokens.filter((t) => t.id !== open.token.id));
            setDone(`Revoked “${open.token.name}”. It no longer works.`);
            close();
          }}
        />
      ) : null}
      {open.kind === 'reveal' ? (
        <ShownOnceSecretDialog
          title="Copy your new team token"
          label="Team token"
          noun="token"
          value={open.created.token}
          onDone={close}
        >
          <p className="token-details">
            {open.created.apiToken.name} · {scopesText(open.created.apiToken)} ·{' '}
            {open.created.apiToken.expiresAt === undefined
              ? 'Never expires'
              : `Expires ${formatDate(open.created.apiToken.expiresAt)}`}
          </p>
        </ShownOnceSecretDialog>
      ) : null}
    </Card>
  );
}

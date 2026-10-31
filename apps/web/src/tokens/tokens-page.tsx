// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  EmptyState,
  Notice,
  PageHeader,
  StatusBadge,
  Table,
  type TableColumn,
} from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import { useTitle } from '../shell/page.js';
import { Time } from '../sites/format.js';
import { CreateTokenDialog } from './create-token.js';
import {
  expiringSoon,
  formatDate,
  scopesText,
  type ApiToken,
  type CreatedApiToken,
} from './format.js';
import { RevokeTokenDialog } from './revoke-token.js';
import { ShownOnceSecretDialog } from './shown-once-secret.js';
import './tokens.css';

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; tokens: ApiToken[]; nextCursor: string | undefined };

type Open =
  | { kind: 'none' }
  | { kind: 'create' }
  | { kind: 'revoke'; token: ApiToken }
  // The one-time response: held only while its dialog is open.
  | { kind: 'reveal'; created: CreatedApiToken };

function Expiry({ token }: { token: ApiToken }) {
  if (token.expiresAt === undefined) return 'Never';
  return (
    <span className="token-expiry">
      {formatDate(token.expiresAt)}
      {expiringSoon(token) && (
        <StatusBadge tone="warning">Expires soon</StatusBadge>
      )}
    </span>
  );
}

/**
 * `/tokens`: the owner's usable API tokens, newest first: create one (shown
 * once) and revoke them. A token cannot do any of this itself, so these stay
 * here and out of the CLI, apart from `wardlock token create`.
 */
export function ApiTokensPage({ api }: { api: ApiClient }) {
  useTitle('API tokens');
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [open, setOpen] = useState<Open>({ kind: 'none' });
  const [done, setDone] = useState<string | null>(null);
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  const [attempt, setAttempt] = useState(0);
  // Bumped by every change, so an older page requested before it cannot
  // put back a revoked token.
  const generation = useRef(0);

  useEffect(() => {
    let live = true;
    api.GET('/api/v0/tokens').then(
      ({ data }) =>
        live &&
        setState(
          data
            ? { kind: 'ready', tokens: data.items, nextCursor: data.nextCursor }
            : { kind: 'error' },
        ),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

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
      .GET('/api/v0/tokens', {
        params: { query: { cursor: state.nextCursor } },
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

  const columns: TableColumn<ApiToken>[] = [
    {
      header: 'Name',
      cell: (token) => (
        <span className="token-name">
          {token.name}
          <code className="token-id">{token.publicId}</code>
        </span>
      ),
    },
    { header: 'Scopes', nowrap: true, cell: scopesText },
    {
      header: 'Created',
      nowrap: true,
      cell: (token) => formatDate(token.createdAt),
    },
    {
      header: 'Expires',
      nowrap: true,
      cell: (token) => <Expiry token={token} />,
    },
    {
      header: 'Last used',
      nowrap: true,
      cell: (token) =>
        token.lastUsedAt === undefined ? (
          'Never'
        ) : (
          <Time at={token.lastUsedAt} />
        ),
    },
    {
      header: 'Actions',
      cell: (token) => (
        <Button
          type="button"
          aria-label={`Revoke ${token.name}`}
          onClick={() => {
            setDone(null);
            setOpen({ kind: 'revoke', token });
          }}
        >
          Revoke
        </Button>
      ),
    },
  ];

  let body;
  if (state.kind === 'loading') body = <p role="status">Loading API tokens…</p>;
  else if (state.kind === 'error')
    body = (
      <div className="token-error">
        <Notice tone="danger">
          Could not load your API tokens. Check your connection, then try again.
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
    );
  else if (state.tokens.length === 0)
    body = (
      <EmptyState title="No API tokens yet">
        <p>
          A token lets a CI job or another tool deploy or manage your sites
          without you signing in. Give each one its own, so you can revoke it
          alone.
        </p>
      </EmptyState>
    );
  else
    body = (
      <Table
        caption="API tokens, newest first"
        columns={columns}
        rows={state.tokens}
        rowKey={(token) => token.id}
      />
    );

  return (
    <>
      <PageHeader
        title="API tokens"
        description="Let CI jobs, scripts and other tools use Wardlock as you, with only the access they need."
      />
      <Card title="Your tokens">
        <div className="token-intro">
          <p>
            Revoked tokens stop working at once, and expired ones leave this
            list. In a terminal, <code>wardlock token create</code> makes one
            too.
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
            Create token
          </Button>
        </div>
        {done && <Notice>{done}</Notice>}
        {body}
        {more === 'error' && (
          <Notice tone="danger">Could not load more tokens. Try again.</Notice>
        )}
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
      </Card>

      {open.kind === 'create' && (
        <CreateTokenDialog
          api={api}
          onClose={close}
          onCreated={(created) => {
            update((tokens) => [created.apiToken, ...tokens]);
            setOpen({ kind: 'reveal', created });
          }}
        />
      )}
      {open.kind === 'revoke' && (
        <RevokeTokenDialog
          api={api}
          token={open.token}
          onClose={close}
          onRevoked={() => {
            update((tokens) => tokens.filter((t) => t.id !== open.token.id));
            setDone(`Revoked “${open.token.name}”. It no longer works.`);
            close();
          }}
        />
      )}
      {open.kind === 'reveal' && (
        <ShownOnceSecretDialog
          title="Copy your new token"
          label="API token"
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
      )}
    </>
  );
}

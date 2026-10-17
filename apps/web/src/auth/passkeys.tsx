// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { Button, Card, Notice, PageHeader } from '@wardlock/ui';
import { useEffect, useRef, useState } from 'react';
import { useTitle } from '../shell/page.js';
import { failure, unreachable } from './messages.js';
import {
  creationOptions,
  credentialJSON,
  deviceName,
  passkeyFailure,
  passkeysSupported,
} from './webauthn.js';

type Passkey = components['schemas']['Passkey'];

type ListState =
  { kind: 'loading' } | { kind: 'error' } | { kind: 'ready'; items: Passkey[] };

type Outcome = { tone: 'info' | 'danger'; text: string };

const dateFormat = new Intl.DateTimeFormat('en-NZ', {
  day: 'numeric',
  month: 'short',
  year: 'numeric',
});
const formatDate = (ms: number) => dateFormat.format(new Date(ms));

/**
 * `/passkeys`: the signed-in owner's passkeys, with adding and removing.
 * Passkeys belong to the dashboard's address, so they are managed here and
 * not from the CLI.
 */
export function PasskeysPage({ api }: { api: ApiClient }) {
  useTitle('Passkeys');
  const [list, setList] = useState<ListState>({ kind: 'loading' });
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [adding, setAdding] = useState(false);
  const supported = passkeysSupported();

  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/passkeys')
      .then(({ data }) => {
        if (live)
          setList(
            data ? { kind: 'ready', items: data.items } : { kind: 'error' },
          );
      })
      .catch(() => {
        if (live) setList({ kind: 'error' });
      });
    return () => {
      live = false;
    };
  }, [api]);

  // Adding and removing answer with what changed, so the list is updated
  // in place rather than fetched again.
  const update = (change: (items: Passkey[]) => Passkey[]) =>
    setList((current) =>
      current.kind === 'ready'
        ? { kind: 'ready', items: change(current.items) }
        : current,
    );

  const add = async () => {
    setAdding(true);
    setOutcome(null);
    try {
      const start = await api.POST('/api/v0/passkeys/registration');
      if (!start.data) {
        setOutcome({
          tone: 'danger',
          text:
            start.error?.code === 'passkey_limit'
              ? start.error.message
              : failure(start.response),
        });
        return;
      }
      let credential: Credential | null;
      try {
        credential = await navigator.credentials.create({
          publicKey: creationOptions(start.data.options),
        });
      } catch (err) {
        setOutcome({ tone: 'danger', text: passkeyFailure(err, 'add') });
        return;
      }
      if (!(credential instanceof PublicKeyCredential)) {
        setOutcome({ tone: 'danger', text: passkeyFailure(null, 'add') });
        return;
      }
      const { data, error, response } = await api.POST('/api/v0/passkeys', {
        body: {
          ceremonyId: start.data.ceremonyId,
          name: deviceName(navigator.userAgent),
          credential: credentialJSON(credential),
        },
      });
      if (data) {
        setOutcome({
          tone: 'info',
          text: `Passkey added as “${data.name}”. Next time, choose Sign in with a passkey.`,
        });
        update((items) => [...items, data]);
        return;
      }
      setOutcome({
        tone: 'danger',
        text:
          error?.code === 'invalid_passkey'
            ? 'The passkey was not accepted. Try adding it again.'
            : error?.code === 'passkey_limit'
              ? error.message
              : failure(response),
      });
    } catch {
      setOutcome({ tone: 'danger', text: unreachable });
    } finally {
      setAdding(false);
    }
  };

  const removed = (passkey: Passkey) => {
    setOutcome({ tone: 'info', text: `Passkey “${passkey.name}” removed.` });
    update((items) => items.filter((p) => p.id !== passkey.id));
  };

  return (
    <>
      <PageHeader
        title="Passkeys"
        description="Sign in with your device’s screen lock, a password manager or a security key instead of an email code."
      />
      {outcome ? <Notice tone={outcome.tone}>{outcome.text}</Notice> : null}
      <Card title="Your passkeys">
        {list.kind === 'loading' && <p role="status">Loading passkeys…</p>}
        {list.kind === 'error' && (
          <Notice tone="danger">
            Could not load your passkeys. Reload the page to try again.
          </Notice>
        )}
        {list.kind === 'ready' && list.items.length === 0 && (
          <p className="passkey-empty">
            You have no passkeys yet. Until you add one, you sign in with a code
            sent to your email.
          </p>
        )}
        {list.kind === 'ready' && list.items.length > 0 && (
          <ul className="passkey-list">
            {list.items.map((passkey) => (
              <PasskeyRow
                key={passkey.id}
                api={api}
                passkey={passkey}
                onRemoved={removed}
                onError={(text) => setOutcome({ tone: 'danger', text })}
              />
            ))}
          </ul>
        )}
        {supported ? (
          <div className="passkey-add">
            <Button
              type="button"
              variant="primary"
              disabled={adding || list.kind !== 'ready'}
              onClick={() => void add()}
            >
              {adding ? 'Waiting for your passkey…' : 'Add a passkey'}
            </Button>
          </div>
        ) : (
          <Notice tone="info">
            This browser cannot create passkeys. Use a current version of
            Chrome, Edge, Firefox or Safari to add one.
          </Notice>
        )}
      </Card>
    </>
  );
}

function PasskeyRow({
  api,
  passkey,
  onRemoved,
  onError,
}: {
  api: ApiClient;
  passkey: Passkey;
  onRemoved: (passkey: Passkey) => void;
  onError: (message: string) => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const row = useRef<HTMLLIElement>(null);
  const titleId = `passkey-${passkey.id}`;

  // Keep focus with the row as it switches between its two states.
  const firstRender = useRef(true);
  useEffect(() => {
    if (firstRender.current) firstRender.current = false;
    else
      row.current
        ?.querySelector<HTMLButtonElement>('button[data-autofocus]')
        ?.focus();
  }, [confirming]);

  const remove = async () => {
    setBusy(true);
    try {
      const { response } = await api.DELETE('/api/v0/passkeys/{passkeyId}', {
        params: { path: { passkeyId: passkey.id } },
      });
      // 404: already gone, which is what was asked for.
      if (response.ok || response.status === 404) {
        onRemoved(passkey);
        return;
      }
      onError(failure(response));
    } catch {
      onError(unreachable);
    }
    setBusy(false);
    setConfirming(false);
  };

  return (
    <li className="passkey-row" aria-labelledby={titleId} ref={row}>
      <div className="passkey-details">
        <span className="passkey-name" id={titleId}>
          {passkey.name}
        </span>
        <span className="passkey-meta">
          Added {formatDate(passkey.createdAt)} ·{' '}
          {passkey.lastUsedAt
            ? `Last used ${formatDate(passkey.lastUsedAt)}`
            : 'Not used yet'}
        </span>
      </div>
      {confirming ? (
        <div className="passkey-confirm">
          <p>
            Remove the passkey “{passkey.name}”? You will no longer be able to
            sign in with it. Your device or password manager keeps its copy
            until you delete it there.
          </p>
          <div className="passkey-actions">
            <Button
              type="button"
              variant="primary"
              disabled={busy}
              onClick={() => void remove()}
            >
              {busy ? 'Removing…' : 'Remove passkey'}
            </Button>
            <Button
              type="button"
              data-autofocus
              disabled={busy}
              onClick={() => setConfirming(false)}
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <Button
          type="button"
          data-autofocus
          aria-label={`Remove ${passkey.name}`}
          onClick={() => setConfirming(true)}
        >
          Remove
        </Button>
      )}
    </li>
  );
}

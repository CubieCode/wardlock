// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, Notice } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import type { Site } from '../sites/format.js';
import type { SignInProvider } from './format.js';
import { PoliciesCard } from './policies.js';

export type ProvidersState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; providers: SignInProvider[] };

/**
 * The Access tab: the site's policies and Check a path, and the sign-in
 * providers this server's operator configured. The providers are read once
 * here, for the card and for the policy editor's Sign in way in; failing to
 * read them leaves the policies working.
 */
export function AccessTab({
  api,
  site,
  onChange,
}: {
  api: ApiClient;
  site: Site;
  onChange: () => void;
}) {
  const [state, setState] = useState<ProvidersState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    api.GET('/api/v0/sign-in-providers').then(
      ({ data }) =>
        live &&
        setState(
          data ? { kind: 'ready', providers: data.items } : { kind: 'error' },
        ),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  return (
    <>
      <PoliciesCard
        api={api}
        site={site}
        onChange={onChange}
        providers={state.kind === 'ready' ? state.providers : undefined}
      />
      <SignInProvidersCard
        state={state}
        onRetry={() => {
          setState({ kind: 'loading' });
          setAttempt((n) => n + 1);
        }}
      />
    </>
  );
}

/** Which accounts the Sign in way in accepts on this server. */
export function SignInProvidersCard({
  state,
  onRetry,
}: {
  state: ProvidersState;
  onRetry: () => void;
}) {
  let body;
  if (state.kind === 'loading')
    body = <p role="status">Loading sign-in providers…</p>;
  else if (state.kind === 'error')
    body = (
      <div className="sites-error">
        <Notice tone="danger">
          Could not load the sign-in providers. Check your connection, then try
          again.
        </Notice>
        <Button type="button" onClick={onRetry}>
          Try again
        </Button>
      </div>
    );
  else if (state.providers.length === 0)
    body = (
      <p>
        This server has no sign-in providers set up, so policies cannot offer
        Sign in as a way in. The server’s operator can add GitHub, Google or a
        single sign-on provider in its configuration.
      </p>
    );
  else
    body = (
      <>
        <p>
          A policy with Sign in lets visitors in with an account from one of
          these providers, set up by the server’s operator. Sign-in and email
          codes share each policy’s allowlist.
        </p>
        <ul className="policy-providers" aria-label="Sign-in providers">
          {state.providers.map((provider) => (
            <li key={provider.id}>{provider.name}</li>
          ))}
        </ul>
      </>
    );
  return <Card title="Sign-in providers">{body}</Card>;
}

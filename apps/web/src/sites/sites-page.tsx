// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, EmptyState, Notice, PageHeader } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { useTitle } from '../shell/page.js';
import { useFlash } from './flash.js';
import {
  defaultPolicy,
  PolicyBadge,
  Time,
  type Policy,
  type Site,
} from './format.js';
import './sites.css';

interface Row {
  site: Site;
  /** Undefined when the site's policies could not be read. */
  policy: Policy | undefined;
}

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | {
      kind: 'ready';
      rows: Row[];
      nextCursor: string | undefined;
      more: MoreState;
    };

type MoreState = 'idle' | 'loading' | 'error';

/** One page of sites, each with its default policy. */
async function loadPage(api: ApiClient, cursor?: string) {
  const { data } = await api.GET('/api/v0/sites', {
    params: { query: cursor ? { cursor } : {} },
  });
  if (!data) throw new Error('could not list sites');
  const rows = await Promise.all(
    data.items.map(async (site): Promise<Row> => {
      const { data: policies } = await api
        .GET('/api/v0/sites/{site}/policies', {
          params: { path: { site: site.name } },
        })
        .catch(() => ({ data: undefined }));
      return { site, policy: policies && defaultPolicy(policies.items) };
    }),
  );
  return { rows, nextCursor: data.nextCursor };
}

/** `/sites`: every site the owner has, with its access and last deploy. */
export function SitesPage({ api }: { api: ApiClient }) {
  useTitle('Sites');
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const flash = useFlash();

  // Each attempt loads the first page again; Try again starts a new one.
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    loadPage(api).then(
      (page) => live && setState({ kind: 'ready', ...page, more: 'idle' }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);
  const retry = () => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  };

  const loadMore = async () => {
    if (state.kind !== 'ready' || !state.nextCursor) return;
    setState({ ...state, more: 'loading' });
    try {
      const page = await loadPage(api, state.nextCursor);
      setState({
        kind: 'ready',
        rows: [...state.rows, ...page.rows],
        nextCursor: page.nextCursor,
        more: 'idle',
      });
    } catch {
      setState({ ...state, more: 'error' });
    }
  };

  return (
    <>
      <PageHeader
        title="Sites"
        description="Each site's address, who can open it and its live deploy."
      />
      {flash ? <Notice>{flash}</Notice> : null}
      {state.kind === 'loading' && <p role="status">Loading sites…</p>}
      {state.kind === 'error' && (
        <div className="sites-error">
          <Notice tone="danger">
            Could not load your sites. Check your connection, then try again.
          </Notice>
          <Button type="button" onClick={retry}>
            Try again
          </Button>
        </div>
      )}
      {state.kind === 'ready' && state.rows.length === 0 && (
        <EmptyState title="No sites yet">
          <p>
            Your sites are listed here, with who can open each one and its live
            deploy.
          </p>
        </EmptyState>
      )}
      {state.kind === 'ready' && state.rows.length > 0 && (
        <>
          <ul className="site-list" aria-label="Sites">
            {state.rows.map(({ site, policy }) => (
              <li key={site.id} className="site-row">
                <div className="site-row-main">
                  <a
                    className="site-row-name"
                    href={`/sites/${encodeURIComponent(site.name)}`}
                  >
                    {site.name}
                  </a>
                  <span className="site-row-host" title={site.hostname}>
                    {site.hostname}
                  </span>
                </div>
                <PolicyBadge policy={policy} />
                <span className="site-row-deploy">
                  {site.activatedAt !== undefined ? (
                    <>
                      Deployed <Time at={site.activatedAt} />
                    </>
                  ) : (
                    'Not deployed yet'
                  )}
                </span>
              </li>
            ))}
          </ul>
          {state.more === 'error' && (
            <Notice tone="danger">Could not load more sites. Try again.</Notice>
          )}
          {state.nextCursor ? (
            <Button
              type="button"
              disabled={state.more === 'loading'}
              onClick={loadMore}
            >
              {state.more === 'loading' ? 'Loading…' : 'Show more sites'}
            </Button>
          ) : null}
        </>
      )}
    </>
  );
}

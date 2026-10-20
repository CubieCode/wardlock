// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, PageHeader, type TableColumn } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { useTitle } from '../shell/page.js';
import {
  useOlderVisits,
  visitColumns,
  VisitsCard,
  type AccessLogEntry,
  type AccessLogPage,
} from './visits.js';
import './sites.css';

type PageState =
  | { kind: 'loading' }
  | { kind: 'not-found' }
  | { kind: 'error' }
  | { kind: 'ready'; page: AccessLogPage };

class NotFound extends Error {}

async function load(api: ApiClient, site: string, cursor?: string) {
  const { data, response } = await api.GET('/api/v0/sites/{site}/access-log', {
    params: { path: { site }, query: cursor ? { cursor } : {} },
  });
  // A missing site and another owner's answer 404, and a name the server
  // refuses to parse 400.
  if ([404, 400].includes(response.status)) throw new NotFound();
  if (!data) throw new Error('could not load the access log');
  return data;
}

const viaText: Record<AccessLogEntry['via'], string> = {
  public: 'Public',
  link: 'Share link',
  password: 'Password',
  email: 'Email code',
  oauth: 'Account sign-in',
  none: 'Not let in',
};

/** How a visitor got in; a share link links to its own access log. */
function Via({ site, entry }: { site: string; entry: AccessLogEntry }) {
  if (entry.via === 'none')
    return <span className="visit-muted">{viaText.none}</span>;
  if (entry.via !== 'link' || !entry.linkPublicId) return viaText[entry.via];
  const href = `/sites/${encodeURIComponent(site)}/links/${entry.linkPublicId}`;
  // One element, so a row stacked on a phone keeps the words together.
  return (
    <span>
      {viaText.link}{' '}
      <a href={href}>{entry.linkLabel ?? <code>{entry.linkPublicId}</code>}</a>
    </span>
  );
}

/** `/sites/:name/access-log`: every visit to one site, newest first. */
export function SiteAccessLogPage({
  api,
  name,
}: {
  api: ApiClient;
  name: string;
}) {
  const [state, setState] = useState<PageState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useTitle(
    state.kind === 'not-found' ? 'Site not found' : `Access log · ${name}`,
  );

  useEffect(() => {
    let live = true;
    load(api, name).then(
      (page) => live && setState({ kind: 'ready', page }),
      (error: unknown) =>
        live &&
        setState({ kind: error instanceof NotFound ? 'not-found' : 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, name, attempt]);

  const back = (
    <p className="site-back">
      <a href={`/sites/${encodeURIComponent(name)}`}>Back to {name}</a>
    </p>
  );

  if (state.kind === 'loading') return <p role="status">Loading access log…</p>;
  if (state.kind === 'not-found')
    return (
      <>
        <PageHeader
          title="Site not found"
          description="There is no site with this name in your account."
        />
        <p>
          <a href="/sites">Go to your sites</a>
        </p>
      </>
    );
  if (state.kind === 'error')
    return (
      <>
        {back}
        <PageHeader title="Access log" />
        <Notice tone="danger">
          Could not load the access log. Check your connection, then try again.
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
      </>
    );

  return (
    <>
      {back}
      <PageHeader
        title="Access log"
        description={`Every request for ${name}, allowed or refused, newest first.`}
      />
      <SiteVisits api={api} name={name} first={state.page} />
    </>
  );
}

function SiteVisits({
  api,
  name,
  first,
}: {
  api: ApiClient;
  name: string;
  first: AccessLogPage;
}) {
  const { entries, nextCursor, more, loadOlder } = useOlderVisits(
    first,
    (cursor) => load(api, name, cursor),
  );
  const columns: TableColumn<AccessLogEntry>[] = [
    visitColumns.time,
    visitColumns.path,
    visitColumns.result,
    { header: 'Got in with', cell: (e) => <Via site={name} entry={e} /> },
    visitColumns.ip,
  ];
  return (
    <VisitsCard
      caption={`Visits to ${name}`}
      columns={columns}
      entries={entries}
      nextCursor={nextCursor}
      more={more}
      onOlder={loadOlder}
      empty={
        <p>
          Each request for this site is listed here, with its time, path,
          result, how the visitor got in and their IP address.
        </p>
      }
    />
  );
}

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, type TableColumn } from '@wardlock/ui';
import { useEffect, useState } from 'react';
import {
  countryColumns,
  useOlderVisits,
  visitColumns,
  VisitsCard,
  type AccessLogEntry,
  type AccessLogPage,
} from './visits.js';
import './sites.css';

type LogState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; page: AccessLogPage };

async function load(api: ApiClient, site: string, cursor?: string) {
  const { data } = await api.GET('/api/v0/sites/{site}/access-log', {
    params: { path: { site }, query: cursor ? { cursor } : {} },
  });
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

/**
 * The Access log tab at `/sites/:name/access-log`: every request for one
 * site, newest first. The site page has already found the site.
 */
export function SiteAccessLogTab({
  api,
  name,
}: {
  api: ApiClient;
  name: string;
}) {
  const [state, setState] = useState<LogState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    load(api, name).then(
      (page) => live && setState({ kind: 'ready', page }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, name, attempt]);

  if (state.kind === 'loading') return <p role="status">Loading access log…</p>;
  if (state.kind === 'error')
    return (
      <div className="sites-error">
        <Notice tone="danger">
          Could not load the access log. Check your connection, then try again.
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

  return (
    <>
      <p className="site-tab-lead">
        Every request for {name}, allowed or refused, newest first.
      </p>
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
    ...countryColumns(entries),
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

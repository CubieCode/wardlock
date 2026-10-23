// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  EmptyState,
  Notice,
  StatusBadge,
  Table,
  type TableColumn,
} from '@wardlock/ui';
import { useEffect, useState, type ReactNode } from 'react';
import {
  Attribution,
  CountryCell,
  type CountryLookup,
} from '../analytics/country.js';
import './sites.css';

export type AccessLogEntry = components['schemas']['AccessLogEntry'];
export type AccessLogPage = components['schemas']['AccessLogPage'];

const fullTime = new Intl.DateTimeFormat('en-NZ', {
  dateStyle: 'medium',
  timeStyle: 'medium',
});

/** Columns shared by the site and share-link access logs. */
export const visitColumns = {
  time: {
    header: 'Time',
    nowrap: true,
    cell: (e) => {
      const date = new Date(e.occurredAt);
      return <time dateTime={date.toISOString()}>{fullTime.format(date)}</time>;
    },
  },
  path: { header: 'Path', cell: (e) => e.path, mono: true },
  result: {
    header: 'Result',
    nowrap: true,
    cell: (e) =>
      e.status < 400 ? (
        <StatusBadge tone="success">Opened · {e.status}</StatusBadge>
      ) : (
        <StatusBadge tone="danger">Refused · {e.status}</StatusBadge>
      ),
  },
  ip: {
    header: 'IP address',
    cell: (e) => e.ip ?? <span className="visit-muted">Not recorded</span>,
    mono: true,
    nowrap: true,
  },
  country: {
    header: 'Country',
    nowrap: true,
    cell: (e) =>
      e.country ? (
        <CountryCell country={e.country} />
      ) : (
        <span className="visit-muted">Not recorded</span>
      ),
  },
  browser: {
    header: 'Browser or client',
    cell: (e) => e.userAgent ?? <span className="visit-muted">None sent</span>,
  },
} satisfies Record<string, TableColumn<AccessLogEntry>>;

/**
 * The site's country lookup, from one small analytics read beside the log:
 * present only while the server's lookup is on. Undefined while it loads,
 * while the lookup is off and if the read fails, so the log never waits on
 * it and then mentions no country.
 */
export function useCountryLookup(api: ApiClient, site: string) {
  // Kept with its site, so another site's lookup is never shown.
  const [read, setRead] = useState<{
    site: string;
    lookup: CountryLookup | undefined;
  }>();
  useEffect(() => {
    let live = true;
    api.GET('/api/v0/analytics', { params: { query: { site, days: 7 } } }).then(
      ({ data }) => live && setRead({ site, lookup: data?.countryLookup }),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [api, site]);
  return read?.site === site ? read.lookup : undefined;
}

/** The Country column, only while the lookup is on. */
export const countryColumns = (lookup: CountryLookup | undefined) =>
  lookup ? [visitColumns.country] : [];

/**
 * One page of entries and the cursor of the next, with a loader for older
 * pages that appends them. `fetchPage` reads the page after a cursor.
 */
export function useOlderVisits(
  initial: AccessLogPage,
  fetchPage: (cursor: string) => Promise<AccessLogPage | undefined>,
) {
  const [page, setPage] = useState({
    entries: initial.items,
    nextCursor: initial.nextCursor,
  });
  const [more, setMore] = useState<'idle' | 'loading' | 'error'>('idle');
  const loadOlder = async () => {
    if (!page.nextCursor) return;
    setMore('loading');
    const data = await fetchPage(page.nextCursor).catch(() => undefined);
    if (!data) return setMore('error');
    setMore('idle');
    setPage((current) => ({
      entries: [...current.entries, ...data.items],
      nextCursor: data.nextCursor,
    }));
  };
  return { ...page, more, loadOlder };
}

/** The "Visits" card: a table of entries, or an empty state, and paging. */
export function VisitsCard({
  caption,
  columns,
  entries,
  nextCursor,
  more,
  onOlder,
  empty,
  lookup,
}: {
  caption: string;
  columns: TableColumn<AccessLogEntry>[];
  entries: AccessLogEntry[];
  nextCursor: string | undefined;
  more: 'idle' | 'loading' | 'error';
  onOlder: () => void;
  /** What the empty state says will appear here. */
  empty: ReactNode;
  /** The country lookup, whose credit goes under the table. */
  lookup?: CountryLookup | undefined;
}) {
  return (
    <Card title="Visits">
      {entries.length === 0 ? (
        <EmptyState title="No visits yet">{empty}</EmptyState>
      ) : (
        <>
          <Table
            caption={caption}
            columns={columns}
            rows={entries}
            rowKey={(e) => e.id}
          />
          {lookup ? <Attribution text={lookup.attribution} /> : null}
        </>
      )}
      {more === 'error' && (
        <Notice tone="danger">Could not load older visits. Try again.</Notice>
      )}
      {nextCursor ? (
        <div>
          <Button type="button" disabled={more === 'loading'} onClick={onOlder}>
            {more === 'loading' ? 'Loading…' : 'Show older visits'}
          </Button>
        </div>
      ) : null}
    </Card>
  );
}

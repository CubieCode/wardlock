// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  EmptyState,
  Notice,
  StatusBadge,
  Table,
  type TableColumn,
} from '@wardlock/ui';
import { useState, type ReactNode } from 'react';
import { CountryCell } from '../analytics/country.js';
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
 * The Country column, only when an entry has a country: the server sends
 * none while its country lookup is off, so then nothing mentions countries.
 */
export const countryColumns = (entries: AccessLogEntry[]) =>
  entries.some((e) => e.country) ? [visitColumns.country] : [];

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
}: {
  caption: string;
  columns: TableColumn<AccessLogEntry>[];
  entries: AccessLogEntry[];
  nextCursor: string | undefined;
  more: 'idle' | 'loading' | 'error';
  onOlder: () => void;
  /** What the empty state says will appear here. */
  empty: ReactNode;
}) {
  return (
    <Card title="Visits">
      {entries.length === 0 ? (
        <EmptyState title="No visits yet">{empty}</EmptyState>
      ) : (
        <Table
          caption={caption}
          columns={columns}
          rows={entries}
          rowKey={(e) => e.id}
        />
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

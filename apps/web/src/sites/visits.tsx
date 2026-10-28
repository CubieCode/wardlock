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
import { useWide } from '../shell/wide.js';
import { readWeek } from './week-analytics.js';
import './sites.css';

export type AccessLogEntry = components['schemas']['AccessLogEntry'];
export type AccessLogPage = components['schemas']['AccessLogPage'];

const fullTime = new Intl.DateTimeFormat('en-NZ', {
  dateStyle: 'medium',
  timeStyle: 'medium',
});

const clock = new Intl.DateTimeFormat('en-NZ', { timeStyle: 'short' });
const dayName = new Intl.DateTimeFormat('en-NZ', {
  weekday: 'long',
  day: 'numeric',
  month: 'long',
});
const dayNameWithYear = new Intl.DateTimeFormat('en-NZ', {
  weekday: 'long',
  day: 'numeric',
  month: 'long',
  year: 'numeric',
});

/** Today, Yesterday, or the date, with its year when it is not this year's. */
export function dayLabel(at: Date, now: Date) {
  const day = (d: Date) =>
    new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((day(now) - day(at)) / 86_400_000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return (
    at.getFullYear() === now.getFullYear() ? dayName : dayNameWithYear
  ).format(at);
}

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
    readWeek(api, site).then(
      (data) => live && setRead({ site, lookup: data.countryLookup }),
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

/** What a request's phone row shows beside its path, time, result and IP. */
export interface VisitRow {
  /** How the visitor got in, after the result. */
  via?: (entry: AccessLogEntry) => ReactNode;
  /** The browser or client, after the IP address. */
  browser?: boolean;
}

/**
 * Below 48rem: the entries grouped by day under a heading each, every
 * request on short lines (path and time; result and way in; IP address and
 * country; the browser when asked for) instead of a stacked table row.
 */
function VisitDays({
  caption,
  entries,
  row,
  country,
}: {
  caption: string;
  entries: AccessLogEntry[];
  row: VisitRow;
  country: boolean;
}) {
  const now = new Date();
  const days: { label: string; entries: AccessLogEntry[] }[] = [];
  for (const entry of entries) {
    const label = dayLabel(new Date(entry.occurredAt), now);
    const last = days.at(-1);
    if (last?.label === label) last.entries.push(entry);
    else days.push({ label, entries: [entry] });
  }
  return (
    <div className="visit-days" role="region" aria-label={caption}>
      {days.map((day, i) => (
        // Entries come newest first, so a label never repeats; the index
        // keeps the key unique all the same.
        <section key={`${i}-${day.label}`} className="visit-day">
          <h3 className="visit-day-label">{day.label}</h3>
          <ul className="visit-list">
            {day.entries.map((e) => {
              const date = new Date(e.occurredAt);
              return (
                <li key={e.id} className="visit">
                  <div className="visit-line">
                    <span className="visit-path">{e.path}</span>
                    <time
                      dateTime={date.toISOString()}
                      title={fullTime.format(date)}
                    >
                      {clock.format(date)}
                    </time>
                  </div>
                  <div className="visit-facts">
                    {visitColumns.result.cell(e)}
                    {row.via && <span>{row.via(e)}</span>}
                  </div>
                  <div className="visit-from">
                    <span className="visit-ip">
                      {e.ip ?? (
                        <span className="visit-muted">IP not recorded</span>
                      )}
                    </span>
                    {country && <span>{visitColumns.country.cell(e)}</span>}
                  </div>
                  {row.browser && e.userAgent && (
                    <div className="visit-browser">{e.userAgent}</div>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}

/**
 * The "Requests" card: a table of entries (below 48rem, rows grouped by
 * day), or an empty state, and paging.
 */
export function VisitsCard({
  caption,
  columns,
  row,
  entries,
  nextCursor,
  more,
  onOlder,
  empty,
  lookup,
}: {
  caption: string;
  columns: TableColumn<AccessLogEntry>[];
  /** What the phone rows show; the columns are for wider screens. */
  row: VisitRow;
  entries: AccessLogEntry[];
  nextCursor: string | undefined;
  more: 'idle' | 'loading' | 'error';
  onOlder: () => void;
  /** What the empty state says will appear here. */
  empty: ReactNode;
  /** The country lookup, whose credit goes under the table. */
  lookup?: CountryLookup | undefined;
}) {
  const wide = useWide();
  return (
    <Card title="Requests">
      {entries.length === 0 ? (
        <EmptyState title="No requests yet">{empty}</EmptyState>
      ) : (
        <>
          {wide ? (
            <Table
              caption={caption}
              columns={columns}
              rows={entries}
              rowKey={(e) => e.id}
            />
          ) : (
            <VisitDays
              caption={caption}
              entries={entries}
              row={row}
              country={lookup !== undefined}
            />
          )}
          {lookup ? <Attribution text={lookup.attribution} /> : null}
        </>
      )}
      {more === 'error' && (
        <Notice tone="danger">Could not load older requests. Try again.</Notice>
      )}
      {nextCursor ? (
        <div>
          <Button type="button" disabled={more === 'loading'} onClick={onOlder}>
            {more === 'loading' ? 'Loading…' : 'Show older requests'}
          </Button>
        </div>
      ) : null}
    </Card>
  );
}

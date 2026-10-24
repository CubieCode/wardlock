// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  BarList,
  Card,
  Table,
  type BarListItem,
  type TableColumn,
} from '@wardlock/ui';
import { lazy, Suspense } from 'react';
import {
  Attribution,
  countryName,
  unknownMeaning,
  type CountryLookup,
} from './country.js';
import type { Analytics } from './data.js';

const CountryMap = lazy(() => import('./country-map.js'));

const count = new Intl.NumberFormat('en-NZ');
const percent = new Intl.NumberFormat('en-NZ', {
  style: 'percent',
  maximumFractionDigits: 1,
});
const requests = (n: number) =>
  `${count.format(n)} ${n === 1 ? 'request' : 'requests'}`;

/** Countries named in the bar list; the rest fold into "N more countries". */
const listed = 8;

interface Row {
  code: string;
  name: string;
  allowed: number;
  refused: number;
  value: number;
}

/**
 * "Where requests come from": requests by country as a bubble map and a bar
 * list of the busiest, with the rest folded in, Unknown last, the database's
 * credit and a table of every country. Render it only while the lookup is
 * on (`lookup` present).
 */
export function CountriesCard({
  countries,
  lookup,
  total,
}: {
  countries: NonNullable<Analytics['countries']>;
  lookup: CountryLookup;
  /** Every request in the range, with a country or not. */
  total: number;
}) {
  const rows: Row[] = countries
    .map((c) => ({
      code: c.country,
      name: c.country === 'unknown' ? 'Unknown' : countryName(c.country),
      allowed: c.allowed,
      refused: c.refused,
      value: c.allowed + c.refused,
    }))
    .filter((row) => row.value > 0);
  const known = rows
    .filter((row) => row.code !== 'unknown')
    .sort((a, b) => b.value - a.value || a.name.localeCompare(b.name));
  const unknown = rows.find((row) => row.code === 'unknown');
  const counted = rows.reduce((sum, row) => sum + row.value, 0);
  const share = (value: number) => percent.format(value / counted);
  const uncounted = total - counted;

  const rest = known.slice(listed);
  const items: BarListItem[] = known.slice(0, listed).map((row) => ({
    key: row.code,
    label: row.name,
    value: row.value,
  }));
  if (rest.length > 0)
    items.push({
      key: 'more',
      label: `${rest.length} more ${rest.length === 1 ? 'country' : 'countries'}`,
      value: rest.reduce((sum, row) => sum + row.value, 0),
    });
  if (unknown)
    items.push({ key: 'unknown', label: 'Unknown', value: unknown.value });

  const columns: TableColumn<Row>[] = [
    { header: 'Country', cell: (row) => row.name },
    {
      header: 'Allowed',
      cell: (row) => count.format(row.allowed),
      nowrap: true,
    },
    {
      header: 'Refused',
      cell: (row) => count.format(row.refused),
      nowrap: true,
    },
    { header: 'Share', cell: (row) => share(row.value), nowrap: true },
  ];

  return (
    <Card title="Where requests come from">
      <p className="analytics-note">
        Requests by country, from each request’s shortened IP address and a
        country database on this server; no address is sent anywhere. VPNs,
        relays and proxies show where their exit is.
      </p>
      {counted === 0 ? (
        <p className="analytics-note">
          No requests in this range have a country.
        </p>
      ) : (
        <>
          <div className="country-grid">
            <Suspense fallback={<div className="country-map" />}>
              <div className="country-map-column">
                <CountryMap
                  server={lookup.serverCountry}
                  countries={known.map((row) => ({
                    code: row.code,
                    value: row.value,
                    detail: `${requests(row.value)} · ${share(row.value)}`,
                  }))}
                />
              </div>
            </Suspense>
            <div className="country-list">
              <BarList
                label="Requests by country"
                total={counted}
                items={items}
              />
              {unknown ? (
                <p className="analytics-note">Unknown: {unknownMeaning}</p>
              ) : null}
            </div>
          </div>
          <details className="wl-chart-table">
            <summary>View as table</summary>
            <Table
              caption="Requests by country"
              columns={columns}
              rows={unknown ? [...known, unknown] : known}
              rowKey={(row) => row.code}
            />
          </details>
        </>
      )}
      {uncounted > 0 ? (
        <p className="analytics-note">
          {requests(uncounted)} logged while the country lookup was off have no
          country and are not counted here.
        </p>
      ) : null}
      <Attribution text={lookup.attribution} />
    </Card>
  );
}

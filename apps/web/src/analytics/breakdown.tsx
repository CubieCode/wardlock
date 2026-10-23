// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  BarList,
  Card,
  Table,
  type TableColumn,
  type TagSlot,
} from '@wardlock/ui';
import type { ReactNode } from 'react';

const count = new Intl.NumberFormat('en-NZ');

export interface BreakdownRow {
  key: string;
  label: string;
  /** Short text after the label, such as the site a path belongs to. */
  hint?: string | undefined;
  href?: string | undefined;
  slot?: TagSlot | undefined;
  allowed: number;
  refused: number;
}

/**
 * A card of requests split one way: busiest first as a bar list, and the
 * same rows, allowed and refused, behind "View as table". `allowedOnly`
 * counts only allowed requests, for splits refused requests have no part in.
 */
export function Breakdown({
  title,
  description,
  nameHeader,
  hintHeader,
  rows,
  total,
  allowedOnly = false,
  limit,
  empty,
  footnote,
}: {
  title: string;
  description: ReactNode;
  /** The table's first column heading, such as "Site". */
  nameHeader: string;
  /** The table heading for each row's hint; without it hints stay in the list. */
  hintHeader?: string | undefined;
  rows: BreakdownRow[];
  /** The whole the bars are shares of; leave it out when they overlap. */
  total?: number;
  allowedOnly?: boolean;
  /** Show only the busiest few. */
  limit?: number;
  /** What the card says when no row has requests. */
  empty: string;
  footnote?: ReactNode | undefined;
}) {
  const value = (row: BreakdownRow) =>
    allowedOnly ? row.allowed : row.allowed + row.refused;
  const shown = rows
    .filter((row) => value(row) > 0)
    .sort((a, b) => value(b) - value(a) || a.label.localeCompare(b.label))
    .slice(0, limit);
  const columns: TableColumn<BreakdownRow>[] = [
    { header: nameHeader, cell: (row) => row.label },
    ...(hintHeader
      ? [{ header: hintHeader, cell: (row: BreakdownRow) => row.hint }]
      : []),
    ...(allowedOnly
      ? [
          {
            header: 'Requests',
            cell: (row: BreakdownRow) => count.format(row.allowed),
            nowrap: true,
          },
        ]
      : [
          {
            header: 'Allowed',
            cell: (row: BreakdownRow) => count.format(row.allowed),
            nowrap: true,
          },
          {
            header: 'Refused',
            cell: (row: BreakdownRow) => count.format(row.refused),
            nowrap: true,
          },
        ]),
  ];

  return (
    <Card title={title}>
      <p className="analytics-note">{description}</p>
      {shown.length === 0 ? (
        <p className="analytics-note">{empty}</p>
      ) : (
        <>
          <BarList
            label={title}
            {...(total === undefined ? {} : { total })}
            items={shown.map(({ key, label, hint, href, slot, ...row }) => ({
              key,
              label,
              ...(hint === undefined ? {} : { hint }),
              ...(href === undefined ? {} : { href }),
              ...(slot === undefined ? {} : { slot }),
              value: value({ key, label, ...row }),
            }))}
          />
          <details className="wl-chart-table">
            <summary>View as table</summary>
            <Table
              caption={title}
              columns={columns}
              rows={shown}
              rowKey={(row) => row.key}
            />
          </details>
        </>
      )}
      {footnote ? <p className="analytics-note">{footnote}</p> : null}
    </Card>
  );
}

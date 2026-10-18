// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ReactNode } from 'react';
import { cx } from './cx.js';

export interface TableColumn<Row> {
  /** Column heading; also labels the value when rows stack on small screens. */
  header: string;
  cell: (row: Row) => ReactNode;
  /** Monospace values such as paths, IDs and addresses. */
  mono?: boolean;
  /** Short values (times, statuses, addresses) kept on one line from 48rem. */
  nowrap?: boolean;
}

/**
 * A captioned data table for records read side by side (log entries,
 * histories). Below 48rem each row stacks into a block of labelled values,
 * so the page never scrolls sideways. Changing a table's display drops its
 * semantics in some browsers, so the elements also state their roles and
 * screen readers keep its rows and headers.
 */
export function Table<Row>({
  caption,
  columns,
  rows,
  rowKey,
}: {
  /** Names the table for assistive technology; visually hidden. */
  caption: string;
  columns: TableColumn<Row>[];
  rows: Row[];
  rowKey: (row: Row) => string;
}) {
  return (
    <div className="wl-table-frame">
      <table className="wl-table" role="table">
        <caption className="wl-visually-hidden">{caption}</caption>
        <thead role="rowgroup">
          <tr role="row">
            {columns.map((column) => (
              <th key={column.header} scope="col" role="columnheader">
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody role="rowgroup">
          {rows.map((row) => (
            <tr key={rowKey(row)} role="row">
              {columns.map((column) => (
                <td
                  key={column.header}
                  role="cell"
                  data-label={column.header}
                  className={
                    cx(
                      column.mono ? 'wl-table-mono' : null,
                      column.nowrap ? 'wl-table-nowrap' : null,
                    ) || undefined
                  }
                >
                  {column.cell(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

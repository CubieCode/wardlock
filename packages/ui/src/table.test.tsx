// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { Table } from './index.js';

afterEach(cleanup);

interface Visit {
  id: string;
  path: string;
  status: number;
}

const rows: Visit[] = [
  { id: 'b', path: '/docs/', status: 200 },
  { id: 'a', path: '/', status: 404 },
];

describe('Table', () => {
  it('is a captioned table with column headers and labelled cells', () => {
    render(
      <Table
        caption="Visits"
        rows={rows}
        rowKey={(row) => row.id}
        columns={[
          { header: 'Path', cell: (row) => row.path, mono: true },
          {
            header: 'Status',
            cell: (row) => String(row.status),
            nowrap: true,
          },
        ]}
      />,
    );
    const table = screen.getByRole('table', { name: 'Visits' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((th) => th.textContent),
    ).toEqual(['Path', 'Status']);
    const [, first, second] = within(table).getAllByRole('row');
    const cells = within(first!).getAllByRole('cell');
    expect(cells.map((td) => td.textContent)).toEqual(['/docs/', '200']);
    // Stacked rows label each value from its column.
    expect(cells.map((td) => td.dataset.label)).toEqual(['Path', 'Status']);
    expect(cells[0]!.className).toBe('wl-table-mono');
    expect(cells[1]!.className).toBe('wl-table-nowrap');
    expect(within(second!).getAllByRole('cell')[1]!.textContent).toBe('404');
  });
});

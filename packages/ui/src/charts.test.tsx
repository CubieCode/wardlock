// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { niceScale } from './charts.js';
import { BarList, Sparkline, StackedBars } from './index.js';

afterEach(cleanup);

describe('BarList', () => {
  const items = [
    { key: 'a', label: 'alpha', value: 1200, hint: 'alpha.example.test' },
    { key: 'b', label: 'beta', value: 300, href: '/b', slot: 2 as const },
  ];

  it('labels each bar with its value and share, scaled to the largest', () => {
    render(<BarList label="Top items" items={items} total={2000} />);
    const list = screen.getByRole('list', { name: 'Top items' });
    const [alpha, beta] = within(list).getAllByRole('listitem');
    expect(alpha!.textContent).toContain('alpha');
    expect(alpha!.textContent).toContain('1,200');
    expect(alpha!.textContent).toContain('60%');
    const fill = (item: HTMLElement) =>
      item.querySelector('.wl-bar-fill') as HTMLElement;
    expect(fill(alpha!).style.width).toBe('100%');
    expect(fill(alpha!).style.background).toBe('var(--color-chart-allowed)');
    expect(fill(beta!).style.width).toBe('25%');
    expect(fill(beta!).style.background).toBe('var(--color-tag-2)');
    expect(
      (alpha!.querySelector('.wl-bar-track') as HTMLElement).getAttribute(
        'aria-hidden',
      ),
    ).toBe('true');
  });

  it('shows a tooltip on keyboard focus and hides it on Escape and blur', () => {
    render(<BarList label="Top items" items={items} total={2000} />);
    const [alpha] = screen.getAllByRole('listitem');
    expect(alpha!.tabIndex).toBe(0);
    expect(screen.queryByRole('tooltip')).toBeNull();
    fireEvent.focus(alpha!);
    const tip = screen.getByRole('tooltip');
    expect(alpha!.getAttribute('aria-describedby')).toBe(tip.id);
    expect(tip.textContent).toBe('alpha1,200 · 60%');
    fireEvent.keyDown(alpha!, { key: 'Escape' });
    expect(screen.queryByRole('tooltip')).toBeNull();
    fireEvent.focus(alpha!);
    fireEvent.blur(alpha!);
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('lets a linked bar be reached through its link, not twice', () => {
    render(<BarList label="Top items" items={items} />);
    const link = screen.getByRole('link', { name: 'beta' });
    expect(link.getAttribute('href')).toBe('/b');
    expect(link.closest('li')!.hasAttribute('tabindex')).toBe(false);
    fireEvent.focus(link);
    const tip = screen.getByRole('tooltip');
    expect(link.getAttribute('aria-describedby')).toBe(tip.id);
    // No total: no share.
    expect(tip.textContent).toBe('beta300');
  });
});

describe('Sparkline', () => {
  it('is decorative and draws one line', () => {
    const { container } = render(<Sparkline values={[1, 4, 2, 8]} />);
    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('aria-hidden')).toBe('true');
    expect(svg.querySelectorAll('polyline')).toHaveLength(1);
  });

  it('draws nothing for fewer than two values', () => {
    const { container } = render(<Sparkline values={[3]} />);
    expect(container.querySelector('svg')).toBeNull();
  });
});

describe('axis scale', () => {
  it('keeps whole steps for small counts', () => {
    expect(niceScale(5).ticks).toEqual([0, 2, 4, 6, 8]);
    expect(niceScale(1).ticks).toEqual([0, 1, 2, 3, 4]);
    expect(niceScale(0).ticks).toEqual([0, 1, 2, 3, 4]);
    expect(niceScale(18).ticks).toEqual([0, 5, 10, 15, 20]);
    expect(niceScale(130).ticks).toEqual([0, 40, 80, 120, 160]);
    for (const max of [2, 3, 5, 7, 9, 11, 14])
      expect(niceScale(max).ticks.every(Number.isInteger)).toBe(true);
  });
});

describe('StackedBars', () => {
  const points = [
    { key: 'd1', label: 'Mon 3 Nov', tick: '3 Nov', values: [120, 4] },
    { key: 'd2', label: 'Tue 4 Nov', tick: '4 Nov', values: [80, 0] },
    { key: 'd3', label: 'Wed 5 Nov', tick: '5 Nov', values: [0, 9] },
  ] as { key: string; label: string; tick: string; values: [number, number] }[];

  const chart = () =>
    render(
      <StackedBars
        label="Requests per day"
        series={['Allowed', 'Refused']}
        points={points}
        periodHeader="Day"
      />,
    );

  it('names the chart, gives each period its values and draws a legend', () => {
    chart();
    expect(
      screen.getByRole('figure', { name: 'Requests per day' }),
    ).toBeTruthy();
    const plot = screen.getByRole('group', { name: 'Requests per day' });
    const cols = within(plot).getAllByRole('img');
    expect(cols.map((c) => c.getAttribute('aria-label'))).toEqual([
      'Mon 3 Nov: 120 Allowed, 4 Refused',
      'Tue 4 Nov: 80 Allowed, 0 Refused',
      'Wed 5 Nov: 0 Allowed, 9 Refused',
    ]);
    // Zero values draw no segment.
    expect(cols[1]!.querySelectorAll('span')).toHaveLength(1);
    expect(
      screen.getByText('Allowed', { selector: '.wl-legend span' }),
    ).toBeTruthy();
    // Axis ticks: a rounded maximum in four steps.
    const y = document.querySelector('.wl-stack-y')!;
    expect([...y.querySelectorAll('span')].map((s) => s.textContent)).toEqual([
      '0',
      '40',
      '80',
      '120',
      '160',
    ]);
  });

  it('is one Tab stop whose arrow keys move a tooltip between periods', () => {
    chart();
    const plot = screen.getByRole('group', { name: 'Requests per day' });
    const cols = within(plot).getAllByRole('img');
    expect(cols.map((c) => c.tabIndex)).toEqual([-1, -1, 0]);
    act(() => cols[2]!.focus());
    expect(screen.getByRole('tooltip').textContent).toBe(
      'Wed 5 Nov' + 'Allowed 0' + 'Refused 9',
    );
    fireEvent.keyDown(plot, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(cols[1]);
    expect(screen.getByRole('tooltip').textContent).toContain('Tue 4 Nov');
    fireEvent.keyDown(plot, { key: 'Home' });
    expect(document.activeElement).toBe(cols[0]);
    expect(cols.map((c) => c.tabIndex)).toEqual([0, -1, -1]);
    fireEvent.keyDown(plot, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(cols[0]);
    fireEvent.keyDown(plot, { key: 'End' });
    expect(document.activeElement).toBe(cols[2]);
    fireEvent.keyDown(plot, { key: 'Escape' });
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('shows the tooltip on hover', () => {
    chart();
    const cols = screen.getAllByRole('img');
    fireEvent.pointerEnter(cols[0]!);
    expect(screen.getByRole('tooltip').textContent).toContain('Mon 3 Nov');
    fireEvent.pointerLeave(
      screen.getByRole('group', { name: 'Requests per day' }),
    );
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('offers the numbers as a table', () => {
    chart();
    const summary = screen.getByText('View as table');
    expect(summary.tagName).toBe('SUMMARY');
    const table = screen.getByRole('table', {
      name: 'Requests per day',
      hidden: true,
    });
    const headers = within(table)
      .getAllByRole('columnheader', { hidden: true })
      .map((h) => h.textContent);
    expect(headers).toEqual(['Day', 'Allowed', 'Refused']);
    expect(within(table).getAllByRole('row', { hidden: true })).toHaveLength(4);
  });
});

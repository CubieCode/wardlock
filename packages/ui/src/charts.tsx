// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useId, useState, type CSSProperties, type ReactNode } from 'react';
import { cx } from './cx.js';
import { Table } from './table.js';
import type { TagSlot } from './tag.js';

const number = new Intl.NumberFormat('en-NZ');
const compact = new Intl.NumberFormat('en-NZ', {
  notation: 'compact',
  maximumFractionDigits: 1,
});
const formatNumber = (value: number) => number.format(value);

/** Which colour of the chart pair a series draws in. */
export type ChartSeries = 1 | 2;
const seriesColour = (series: ChartSeries) =>
  series === 1 ? 'var(--color-chart-allowed)' : 'var(--color-chart-refused)';

function Tooltip({
  id,
  shown,
  style,
  children,
}: {
  id: string;
  shown: boolean;
  style?: CSSProperties;
  children: ReactNode;
}) {
  return (
    <span
      id={id}
      role="tooltip"
      className="wl-tooltip"
      hidden={!shown}
      style={style}
    >
      {children}
    </span>
  );
}

export interface BarListItem {
  key: string;
  label: string;
  value: number;
  /** Makes the label a link, such as to a filtered view. */
  href?: string;
  /** Short mono text after the label, such as an address. */
  hint?: string;
  /** Draws this bar in a tag colour instead of the list's series colour. */
  slot?: TagSlot;
}

/**
 * Labelled horizontal bars, largest first as given, each with its value and,
 * when `total` is set, its share. Bars are scaled to the largest value. Each
 * row is keyboard-focusable (or its link is) and shows a tooltip with the
 * name, value and share on focus or hover; Escape hides it.
 */
export function BarList({
  label,
  items,
  total,
  series = 1,
  formatValue = formatNumber,
}: {
  /** Names the list. */
  label: string;
  items: BarListItem[];
  /** The whole the values are part of; adds each item's share. */
  total?: number;
  series?: ChartSeries;
  formatValue?: (value: number) => string;
}) {
  const id = useId();
  const [active, setActive] = useState<string | null>(null);
  const top = Math.max(1, ...items.map((item) => item.value));
  const share = (value: number) =>
    total ? `${Math.round((value / total) * 100)}%` : null;

  return (
    <ul className="wl-bar-list" aria-label={label}>
      {items.map((item) => {
        const tipId = `${id}-${item.key}`;
        const itemShare = share(item.value);
        const show = () => setActive(item.key);
        const focusable = {
          'aria-describedby': tipId,
          onFocus: show,
          onBlur: () => setActive(null),
        };
        return (
          <li
            key={item.key}
            className="wl-bar-item"
            tabIndex={item.href ? undefined : 0}
            {...(item.href ? {} : focusable)}
            onPointerEnter={show}
            onPointerLeave={() => setActive(null)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') setActive(null);
            }}
          >
            <span className="wl-bar-name">
              {item.href ? (
                <a href={item.href} {...focusable}>
                  {item.label}
                </a>
              ) : (
                item.label
              )}
              {item.hint ? (
                <span className="wl-bar-hint">{item.hint}</span>
              ) : null}
            </span>
            <span className="wl-bar-value">
              {formatValue(item.value)}
              {itemShare ? (
                <span className="wl-bar-share">{itemShare}</span>
              ) : null}
            </span>
            <span className="wl-bar-track" aria-hidden="true">
              <span
                className="wl-bar-fill"
                style={{
                  width: `${(item.value / top) * 100}%`,
                  background:
                    item.slot === undefined
                      ? seriesColour(series)
                      : `var(--color-tag-${item.slot})`,
                }}
              />
            </span>
            <Tooltip id={tipId} shown={active === item.key}>
              <b>{item.label}</b>
              <span>
                {formatValue(item.value)}
                {itemShare ? ` · ${itemShare}` : null}
              </span>
            </Tooltip>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * A small trend line, decorative only: put the figure it summarises in text
 * beside it. Draws nothing for fewer than two values.
 */
export function Sparkline({ values }: { values: number[] }) {
  if (values.length < 2) return null;
  const max = Math.max(1, ...values);
  const points = values
    .map(
      (value, i) =>
        `${((i / (values.length - 1)) * 100).toFixed(2)},${(22 - (value / max) * 20).toFixed(2)}`,
    )
    .join(' ');
  return (
    <svg
      className="wl-sparkline"
      viewBox="0 0 100 24"
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
    >
      <polyline points={points} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

/**
 * A rounded axis maximum and its four equal steps. The charts count whole
 * things (requests), so a fractional step is rounded up to a whole one.
 */
export function niceScale(max: number) {
  const raw = Math.max(1, max) / 4;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const nice =
    [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]
      .map((m) => m * magnitude)
      .find((s) => s >= raw) ?? 10 * magnitude;
  const step = Number.isInteger(nice) ? nice : Math.ceil(nice);
  return { top: step * 4, ticks: [0, 1, 2, 3, 4].map((k) => k * step) };
}

/** Up to five evenly spaced indices, always including the first and last. */
function tickIndices(count: number) {
  if (count <= 5) return [...Array(count).keys()];
  return [0, 1, 2, 3, 4].map((k) => Math.round((k * (count - 1)) / 4));
}

export interface StackedBarsPoint {
  key: string;
  /** Full name of the period, such as "Mon 3 Nov"; used in tooltips and the table. */
  label: string;
  /** Short axis label, such as "3 Nov". Defaults to `label`. */
  tick?: string;
  values: [number, number];
}

/**
 * A time series of two stacked series (the first at the base), with a
 * legend, y-axis ticks, up to five x-axis ticks (three below 48rem) and a "View as table"
 * disclosure. The plot is one Tab stop: arrow keys, Home and End move
 * between periods, and the focused or hovered period shows a tooltip with
 * both values; Escape hides it.
 */
export function StackedBars({
  label,
  series,
  points,
  periodHeader = 'Period',
  formatValue = formatNumber,
}: {
  /** Names the chart and its table. */
  label: string;
  /** The two series' names, base first. */
  series: [string, string];
  points: StackedBarsPoint[];
  /** The table's first column heading, such as "Day". */
  periodHeader?: string;
  formatValue?: (value: number) => string;
}) {
  const id = useId();
  const [active, setActive] = useState(points.length - 1);
  const [shown, setShown] = useState(false);
  const { top, ticks } = niceScale(
    Math.max(0, ...points.map((p) => p.values[0] + p.values[1])),
  );
  const at = Math.min(active, points.length - 1);
  const current = points[at];
  const describe = (point: StackedBarsPoint) =>
    `${point.label}: ${formatValue(point.values[0])} ${series[0]}, ${formatValue(point.values[1])} ${series[1]}`;
  const focusColumn = (plot: HTMLElement, index: number) => {
    setActive(index);
    setShown(true);
    plot.querySelectorAll<HTMLElement>('.wl-stack-col')[index]?.focus();
  };
  const place = points.length > 1 ? at / (points.length - 1) : 0.5;
  const tipStyle: CSSProperties = {
    left: `${((at + 0.5) / Math.max(1, points.length)) * 100}%`,
    transform: `translateX(${place < 0.2 ? '0' : place > 0.8 ? '-100%' : '-50%'})`,
  };

  return (
    <figure className="wl-stacked" aria-labelledby={`${id}-caption`}>
      <figcaption id={`${id}-caption`} className="wl-visually-hidden">
        {label}
      </figcaption>
      <div className="wl-legend">
        {series.map((name, i) => (
          <span key={name}>
            <i
              aria-hidden="true"
              style={{ background: seriesColour((i + 1) as ChartSeries) }}
            />
            {name}
          </span>
        ))}
      </div>
      <div className="wl-stack-frame">
        <div className="wl-stack-y" aria-hidden="true">
          {ticks.map((tick) => (
            <span key={tick} style={{ bottom: `${(tick / top) * 100}%` }}>
              {compact.format(tick)}
            </span>
          ))}
        </div>
        <div
          className="wl-stack-plot"
          role="group"
          aria-label={label}
          onPointerLeave={() => setShown(false)}
          onKeyDown={(event) => {
            const last = points.length - 1;
            const moves: Record<string, number> = {
              ArrowRight: Math.min(last, at + 1),
              ArrowLeft: Math.max(0, at - 1),
              Home: 0,
              End: last,
            };
            if (event.key === 'Escape') {
              setShown(false);
              return;
            }
            const next = moves[event.key];
            if (next === undefined) return;
            event.preventDefault();
            focusColumn(event.currentTarget, next);
          }}
        >
          <div className="wl-stack-grid" aria-hidden="true">
            {ticks.map((tick) => (
              <span key={tick} style={{ bottom: `${(tick / top) * 100}%` }} />
            ))}
          </div>
          <div className="wl-stack-cols">
            {points.map((point, i) => (
              <div
                key={point.key}
                className={cx(
                  'wl-stack-col',
                  shown && i === at ? 'wl-stack-col-active' : null,
                )}
                role="img"
                aria-label={describe(point)}
                tabIndex={i === at ? 0 : -1}
                onFocus={() => {
                  setActive(i);
                  setShown(true);
                }}
                onBlur={() => setShown(false)}
                onPointerEnter={() => {
                  setActive(i);
                  setShown(true);
                }}
              >
                {point.values[1] > 0 ? (
                  <span
                    style={{
                      height: `${(point.values[1] / top) * 100}%`,
                      background: seriesColour(2),
                    }}
                  />
                ) : null}
                {point.values[0] > 0 ? (
                  <span
                    style={{
                      height: `${(point.values[0] / top) * 100}%`,
                      background: seriesColour(1),
                    }}
                  />
                ) : null}
              </div>
            ))}
          </div>
          {current ? (
            <Tooltip id={`${id}-tip`} shown={shown} style={tipStyle}>
              <b>{current.label}</b>
              {series.map((name, i) => (
                <span key={name}>
                  <i
                    aria-hidden="true"
                    style={{
                      background: seriesColour((i + 1) as ChartSeries),
                    }}
                  />
                  {name} {formatValue(current.values[i as 0 | 1])}
                </span>
              ))}
            </Tooltip>
          ) : null}
        </div>
        <div className="wl-stack-x" aria-hidden="true">
          {tickIndices(points.length).map((i) => (
            <span
              key={points[i]!.key}
              style={{ left: `${((i + 0.5) / points.length) * 100}%` }}
            >
              {points[i]!.tick ?? points[i]!.label}
            </span>
          ))}
        </div>
      </div>
      <details className="wl-chart-table">
        <summary>View as table</summary>
        <Table
          caption={label}
          columns={[
            { header: periodHeader, cell: (p: StackedBarsPoint) => p.label },
            ...series.map((name, i) => ({
              header: name,
              cell: (p: StackedBarsPoint) => formatValue(p.values[i as 0 | 1]),
            })),
          ]}
          rows={points}
          rowKey={(p) => p.key}
        />
      </details>
    </figure>
  );
}

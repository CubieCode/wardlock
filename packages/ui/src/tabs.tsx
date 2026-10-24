// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useId,
  useLayoutEffect,
  useRef,
  type AnchorHTMLAttributes,
  type ReactNode,
} from 'react';
import { cx } from './cx.js';

/**
 * A row of page tabs. Each tab is a link to its own address, so it is a
 * labelled navigation landmark rather than an ARIA tablist. On narrow screens
 * the row scrolls sideways inside itself, bringing the current tab into view
 * when it is partly hidden (only the row scrolls, never the page).
 */
export function Tabs({
  label,
  children,
}: {
  label: string;
  /** `Tab`s. */
  children: ReactNode;
}) {
  const row = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const nav = row.current;
    const tab = nav?.querySelector('[aria-current="page"]');
    if (!nav || !tab) return;
    const outer = nav.getBoundingClientRect();
    const inner = tab.getBoundingClientRect();
    if (inner.left >= outer.left && inner.right <= outer.right) return;
    // Centred where the row allows, so the tabs either side show too.
    nav.scrollLeft += inner.left - outer.left - (outer.width - inner.width) / 2;
  });
  return (
    <nav ref={row} className="wl-tabs" aria-label={label}>
      <ul>{children}</ul>
    </nav>
  );
}

/**
 * One tab: a link, with `current` marking the page shown and an optional
 * count after its name ("Links 4").
 */
export function Tab({
  current = false,
  count,
  className,
  children,
  ...props
}: AnchorHTMLAttributes<HTMLAnchorElement> & {
  href: string;
  current?: boolean;
  count?: number;
  children: ReactNode;
}) {
  return (
    <li>
      <a
        className={cx('wl-tab', className)}
        aria-current={current ? 'page' : undefined}
        {...props}
      >
        {children}
        {count === undefined ? null : (
          <>
            {' '}
            <span className="wl-tab-count">{count}</span>
          </>
        )}
      </a>
    </li>
  );
}

export interface SegmentedOption<Value extends string> {
  value: Value;
  label: ReactNode;
}

/**
 * A choice of one from two to five short options, drawn as joined segments
 * (a range, a density, a grouping). Native radios underneath: Tab reaches the
 * chosen segment and arrow keys move the choice.
 */
export function SegmentedControl<Value extends string>({
  label,
  value,
  options,
  onChange,
}: {
  /** Names the group; visually hidden, so make it clear from context. */
  label: string;
  value: Value;
  options: SegmentedOption<Value>[];
  onChange: (value: Value) => void;
}) {
  const name = useId();
  return (
    <fieldset className="wl-segmented">
      <legend className="wl-visually-hidden">{label}</legend>
      {options.map((option) => (
        <label key={option.value} className="wl-segment">
          <input
            type="radio"
            className="wl-segment-input"
            name={name}
            value={option.value}
            checked={option.value === value}
            onChange={() => onChange(option.value)}
          />
          <span className="wl-segment-label">{option.label}</span>
        </label>
      ))}
    </fieldset>
  );
}

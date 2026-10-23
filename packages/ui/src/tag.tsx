// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ReactNode } from 'react';
import { cx } from './cx.js';

export type StatusTagTone =
  'neutral' | 'success' | 'warning' | 'danger' | 'ward' | 'public' | 'primary';

/**
 * A short state word in mono capitals, such as LIVE or EXPIRED. The word
 * carries the meaning; the tone supplements it.
 */
export function StatusTag({
  tone = 'neutral',
  children,
}: {
  tone?: StatusTagTone;
  children: ReactNode;
}) {
  return (
    <span className={cx('wl-status-tag', `wl-status-tag-${tone}`)}>
      {children}
    </span>
  );
}

/** One of the nine tag colours: 0 is neutral, 1-8 the categorical order. */
export type TagSlot = 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8;

/**
 * A named label with its colour dot. The dot is decorative and always sits
 * beside the name. With `onRemove`, a small button named "Remove <label>"
 * follows the name. In a narrow space the name ends in an ellipsis, with
 * the whole name on hover.
 */
export function TagChip({
  label,
  slot = 0,
  onRemove,
}: {
  label: string;
  slot?: TagSlot;
  onRemove?: () => void;
}) {
  return (
    <span className="wl-tag-chip">
      <span
        className="wl-tag-dot"
        style={{ background: `var(--color-tag-${slot})` }}
        aria-hidden="true"
      />
      <span className="wl-tag-chip-label" title={label}>
        {label}
      </span>
      {onRemove ? (
        <button
          type="button"
          className="wl-tag-chip-remove"
          aria-label={`Remove ${label}`}
          onClick={onRemove}
        >
          <span aria-hidden="true">×</span>
        </button>
      ) : null}
    </span>
  );
}

/**
 * The ward: the brass diamond that marks protection. Decorative; always put
 * the word for the state beside it.
 */
export function Ward({ size = 'md' }: { size?: 'sm' | 'md' }) {
  return (
    <span className={cx('wl-ward', `wl-ward-${size}`)} aria-hidden="true" />
  );
}

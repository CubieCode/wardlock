// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ReactNode } from 'react';
import { cx } from './cx.js';

export type StatusTone = 'neutral' | 'success' | 'warning' | 'danger';

/** A short status word. Tone supplements the word; it never replaces it. */
export function StatusBadge({
  tone = 'neutral',
  children,
}: {
  tone?: StatusTone;
  children: ReactNode;
}) {
  return <span className={cx('wl-badge', `wl-badge-${tone}`)}>{children}</span>;
}

export type NoticeTone = 'info' | 'warning' | 'danger';

/**
 * Inline guidance. Danger notices are announced immediately (role="alert");
 * others politely (role="status").
 */
export function Notice({
  tone = 'info',
  children,
}: {
  tone?: NoticeTone;
  children: ReactNode;
}) {
  return (
    <div
      role={tone === 'danger' ? 'alert' : 'status'}
      className={cx('wl-notice', `wl-notice-${tone}`)}
    >
      {children}
    </div>
  );
}

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { KeyboardEvent, ReactNode } from 'react';

function moveAlong(event: KeyboardEvent<HTMLDivElement>) {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
  const controls = [
    ...event.currentTarget.querySelectorAll<HTMLElement>(
      'button:not(:disabled)',
    ),
  ];
  const at = controls.indexOf(document.activeElement as HTMLElement);
  if (at < 0) return;
  const step = event.key === 'ArrowRight' ? 1 : -1;
  event.preventDefault();
  controls[(at + step + controls.length) % controls.length]?.focus();
}

/**
 * A toolbar fixed to the foot of the screen while things are selected: how
 * many (with an optional `note`), the actions that apply to all of them,
 * and Clear selection. Renders
 * nothing when `count` is 0. Left and right arrows move between its
 * buttons. Leave room at the foot of the page so it never covers the last
 * row.
 */
export function BulkBar({
  label,
  count,
  note,
  onClear,
  children,
}: {
  /** Names the toolbar, such as "Bulk actions". */
  label: string;
  count: number;
  /** A word on the selection after the count, such as a limit reached. */
  note?: ReactNode;
  onClear: () => void;
  /** Buttons, or `Popover`s whose triggers are buttons. */
  children: ReactNode;
}) {
  if (count === 0) return null;
  return (
    <div
      role="toolbar"
      aria-label={label}
      className="wl-bulk-bar"
      onKeyDown={moveAlong}
    >
      <span className="wl-bulk-count" aria-live="polite">
        {count} selected
        {note ? <span className="wl-bulk-note">{note}</span> : null}
      </span>
      <div className="wl-bulk-actions">
        {children}
        <button
          type="button"
          className="wl-bulk-action wl-bulk-clear"
          onClick={onClear}
        >
          Clear selection
        </button>
      </div>
    </div>
  );
}

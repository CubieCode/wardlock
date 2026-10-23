// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ReactNode } from 'react';
import { cx } from './cx.js';
import { Ward } from './tag.js';

/**
 * A hub node with its sections or rows hanging off it on dashed wires: the
 * Constellation list layout. `hub` is drawn as the glowing node at the top;
 * leave it out to hang rows off whatever sits above (a section heading
 * inside a `Wire`). The children are `Wire`s, in a list. Below 48rem the wires
 * collapse to plain indentation.
 */
export function HubLayout({
  hub,
  label,
  children,
}: {
  hub?: ReactNode;
  /** Names the list of wires when the content above does not. */
  label?: string;
  children: ReactNode;
}) {
  return (
    <div className="wl-hub">
      {hub ? <div className="wl-hub-node">{hub}</div> : null}
      <ul className="wl-wires" aria-label={label}>
        {children}
      </ul>
    </div>
  );
}

/**
 * One section or row on a dashed wire from the hub above. `public` draws the
 * connector in coral; `ward` puts the brass ward on it. Both are decorative,
 * so the content still says what the state is.
 */
export function Wire({
  tone = 'default',
  ward = false,
  children,
}: {
  tone?: 'default' | 'public';
  ward?: boolean;
  children: ReactNode;
}) {
  return (
    <li className={cx('wl-wire', tone === 'public' ? 'wl-wire-public' : null)}>
      {ward ? (
        <span className="wl-wire-ward">
          <Ward size="sm" />
        </span>
      ) : null}
      {children}
    </li>
  );
}

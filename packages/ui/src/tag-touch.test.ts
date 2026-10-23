// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// TagChip's remove button on touch screens. jsdom does no layout, so this
// reads the rule; the dashboard's browser specs measure the hit area.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(
  new URL('../styles/components.css', import.meta.url),
  'utf8',
).replace(/\s+/g, ' ');

describe('TagChip remove button', () => {
  it('takes taps across a full touch target on a coarse pointer, at its drawn size', () => {
    const coarse = css.slice(css.indexOf('@media (pointer: coarse)'));
    expect(coarse).toContain('.wl-tag-chip-remove { position: relative; }');
    expect(coarse).toMatch(
      /\.wl-tag-chip-remove::after \{[^}]*width: var\(--touch-target\); height: var\(--touch-target\); transform: translate\(-50%, -50%\);/,
    );
    // The drawn button itself stays small.
    expect(css).toMatch(
      /\.wl-tag-chip-remove \{[^}]*width: 1\.5rem; height: 1\.5rem;/,
    );
  });
});

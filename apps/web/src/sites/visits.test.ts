// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it } from 'vitest';
import { dayLabel } from './visits.js';

describe('dayLabel', () => {
  const now = new Date(2026, 9, 24, 9, 30);

  it.each([
    ['earlier today', new Date(2026, 9, 24, 0, 5), 'Today'],
    ['late yesterday', new Date(2026, 9, 23, 23, 55), 'Yesterday'],
    ['early yesterday', new Date(2026, 9, 23, 0, 0), 'Yesterday'],
    ['this year', new Date(2026, 9, 22, 17, 5), 'Thursday, 22 October'],
    ['last year', new Date(2025, 11, 30, 12), 'Tuesday, 30 December 2025'],
  ])('names a request from %s', (_, at, label) => {
    expect(dayLabel(at, now)).toBe(label);
  });

  it('counts calendar days across a daylight saving change', () => {
    // New Zealand moves its clocks forward on the last Sunday of September.
    expect(dayLabel(new Date(2026, 8, 26, 23), new Date(2026, 8, 27, 1))).toBe(
      'Yesterday',
    );
  });
});

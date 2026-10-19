// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { act } from '@testing-library/react';
import type { ReactElement } from 'react';

/**
 * Runs the effects React still has queued from the last commit. findBy*
 * resolves as soon as the page is in the DOM, but useEffect callbacks, such as
 * event listeners and timers, run in a later scheduler task that can lose that
 * race under CPU load. React runs them before it starts any new update, so
 * rendering the same element again flushes them and changes nothing else.
 */
export function runEffects(
  rerender: (ui: ReactElement) => void,
  ui: ReactElement,
) {
  act(() => rerender(ui));
}

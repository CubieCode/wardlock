// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useLayoutEffect } from 'react';
import { navigate } from './router.js';

/**
 * Sets the tab title. Titles never carry codes, tokens or addresses. Null
 * leaves the title to a part of the page that sets its own.
 */
export function useTitle(title: string | null) {
  useEffect(() => {
    if (title !== null) document.title = `${title} · Wardlock`;
  }, [title]);
}

/** Replaces the current history entry with `to` before the browser paints. */
export function Redirect({ to }: { to: string }) {
  useLayoutEffect(() => navigate(to, { replace: true }), [to]);
  return null;
}

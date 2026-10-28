// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useSyncExternalStore } from 'react';

// The dashboard's one layout break: from 48rem screens are wide, below it
// they get the phone layouts. Without matchMedia (tests), it is wide.
const wideQuery = '(min-width: 48rem)';

function subscribe(onChange: () => void) {
  if (!window.matchMedia) return () => {};
  const media = window.matchMedia(wideQuery);
  media.addEventListener('change', onChange);
  return () => media.removeEventListener('change', onChange);
}

const snapshot = () =>
  window.matchMedia ? window.matchMedia(wideQuery).matches : true;

/** Whether the screen is at least 48rem wide, kept up to date. */
export function useWide() {
  return useSyncExternalStore(subscribe, snapshot);
}

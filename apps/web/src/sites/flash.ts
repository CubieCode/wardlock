// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from 'react';

// A one-shot message carried to the next screen, such as "docs was torn
// down" after leaving a page that no longer exists. Kept in memory only.
let pending: string | null = null;

export function setFlash(message: string) {
  pending = message;
}

/** The waiting message, shown by this screen and then forgotten. */
export function useFlash() {
  const [message] = useState(() => pending);
  useEffect(() => {
    pending = null;
  }, []);
  return message;
}

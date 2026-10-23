// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, type RefObject } from 'react';

/**
 * Moves focus back to the button that opened an inline form once the form
 * closes, so keyboard users keep their place.
 */
export function useFocusOnReturn(
  button: RefObject<HTMLElement | null>,
  open: boolean,
) {
  const wasOpen = useRef(open);
  useEffect(() => {
    if (wasOpen.current && !open) button.current?.focus();
    wasOpen.current = open;
  }, [button, open]);
}

/**
 * Keeps keyboard users' place when a row is deleted: focus moves to the
 * first action of the next row, else the previous one, else `fallback` (the
 * New button) once the list is empty. Call the answer with the list's IDs
 * before the delete and the deleted ID; rows carry `data-id`.
 */
export function useFocusAfterDelete(
  list: RefObject<HTMLElement | null>,
  fallback: RefObject<HTMLElement | null>,
) {
  // undefined: nothing to do; null: the fallback; else a row's ID.
  const target = useRef<string | null | undefined>(undefined);
  // After every render, so it runs once the deleted row is gone.
  useEffect(() => {
    if (target.current === undefined) return;
    const id = target.current;
    target.current = undefined;
    const row = id
      ? list.current?.querySelector<HTMLElement>(
          `[data-id="${id}"] .organise-actions button`,
        )
      : null;
    (row ?? fallback.current)?.focus();
  });
  return (ids: string[], deleted: string) => {
    const at = ids.indexOf(deleted);
    target.current = ids[at + 1] ?? ids[at - 1] ?? null;
  };
}

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The theme setting, kept per device. Storage can be missing or refuse
// access (private windows, blocked site data), so every read and write is
// guarded and anything unreadable means System.
import { useCallback, useSyncExternalStore } from 'react';

export type Theme = 'system' | 'light' | 'dark';

export const themeStorageKey = 'wardlock-theme';
const changeEvent = 'wardlock:theme';
// What this page chose when storage refused to keep it; gone on reload.
let unstored: Theme | undefined;

/**
 * The theme in use: this page's choice if storage refused to keep it,
 * otherwise the stored one, or System when there is none or it cannot be
 * read.
 */
export function readTheme(): Theme {
  if (unstored) return unstored;
  try {
    const value = window.localStorage.getItem(themeStorageKey);
    return value === 'light' || value === 'dark' ? value : 'system';
  } catch {
    return 'system';
  }
}

/**
 * Forces a theme with `data-theme` on `<html>`, which sets `color-scheme`
 * for every token (docs/DESIGN-SYSTEM.md, "Themes"); System removes it.
 */
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === 'system') delete root.dataset.theme;
  else root.dataset.theme = theme;
}

/** Stores and applies a theme. It still applies when storage refuses it. */
export function setTheme(theme: Theme) {
  try {
    if (theme === 'system') window.localStorage.removeItem(themeStorageKey);
    else window.localStorage.setItem(themeStorageKey, theme);
    unstored = undefined;
  } catch {
    unstored = theme; // Applied for this page only.
  }
  applyTheme(theme);
  window.dispatchEvent(new Event(changeEvent));
}

function subscribe(onChange: () => void) {
  // Another tab changing the setting fires "storage" here.
  const onStorage = (event: StorageEvent) => {
    if (event.key !== themeStorageKey && event.key !== null) return;
    unstored = undefined; // The other tab's choice is the stored one.
    applyTheme(readTheme());
    onChange();
  };
  window.addEventListener('storage', onStorage);
  window.addEventListener(changeEvent, onChange);
  return () => {
    window.removeEventListener('storage', onStorage);
    window.removeEventListener(changeEvent, onChange);
  };
}

/** The current theme setting and a setter, shared by every caller. */
export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, readTheme);
  return [theme, useCallback((next: Theme) => setTheme(next), [])];
}

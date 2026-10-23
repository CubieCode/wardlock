// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Applies a forced theme before the first paint, so it never flashes the
// other one. A classic script, loaded from index.html's head because the
// dashboard's CSP allows no inline script. Keep the key and values in step
// with src/shell/theme.ts.
try {
  const theme = window.localStorage.getItem('wardlock-theme');
  if (theme === 'light' || theme === 'dark')
    document.documentElement.dataset.theme = theme;
} catch {
  // Storage is blocked: System, which needs nothing.
}

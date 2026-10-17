// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// A minimal history router: the dashboard has a handful of routes, so it
// keeps the location in the History API and intercepts same-origin link
// clicks instead of depending on a router library.
import { useEffect, useMemo, useSyncExternalStore } from 'react';

const changeEvent = 'wardlock:navigate';

function subscribe(onChange: () => void) {
  window.addEventListener('popstate', onChange);
  window.addEventListener(changeEvent, onChange);
  return () => {
    window.removeEventListener('popstate', onChange);
    window.removeEventListener(changeEvent, onChange);
  };
}

const snapshot = () => window.location.pathname + window.location.search;

/** The current path and query, re-rendering on navigation. */
export function useLocation() {
  const href = useSyncExternalStore(subscribe, snapshot);
  return useMemo(() => {
    const url = new URL(href, window.location.origin);
    return { pathname: url.pathname, params: url.searchParams };
  }, [href]);
}

/** Moves to a same-origin path. `replace` keeps it out of the back stack. */
export function navigate(to: string, { replace = false } = {}) {
  if (replace) window.history.replaceState(null, '', to);
  else {
    window.history.pushState(null, '', to);
    window.scrollTo(0, 0);
  }
  window.dispatchEvent(new Event(changeEvent));
}

/**
 * Turns plain clicks on same-origin links into client-side navigation, so
 * screens and @wardlock/ui render ordinary anchors.
 */
export function useLinkInterception() {
  useEffect(() => {
    const onClick = (event: MouseEvent) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      const link = (event.target as Element | null)?.closest?.('a');
      if (!link || link.target || link.hasAttribute('download')) return;
      const url = new URL(link.href, window.location.href);
      if (url.origin !== window.location.origin) return;
      event.preventDefault();
      navigate(url.pathname + url.search + url.hash);
    };
    document.addEventListener('click', onClick);
    return () => document.removeEventListener('click', onClick);
  }, []);
}

/**
 * Matches a route pattern such as `/sites/:name` against a path, returning
 * the decoded parameters or null.
 */
export function matchPath(
  pattern: string,
  pathname: string,
): Record<string, string> | null {
  const want = pattern.split('/');
  const got = pathname.replace(/\/+$/, '').split('/');
  if (got.length === 1) got.push(''); // "/" splits to ["", ""] like the pattern.
  if (want.length !== got.length) return null;
  const params: Record<string, string> = {};
  for (const [i, part] of want.entries()) {
    const value = got[i]!;
    if (part.startsWith(':')) {
      if (!value) return null;
      try {
        params[part.slice(1)] = decodeURIComponent(value);
      } catch {
        return null; // Malformed percent-encoding matches nothing.
      }
    } else if (part !== value) return null;
  }
  return params;
}

/**
 * Accepts a return path from the query only when it stays on this origin:
 * a single leading slash, no protocol-relative or backslash tricks, and no
 * control characters (browsers drop tabs and newlines, so "/\t/x" is "//x").
 */
export function safeReturnPath(value: string | null) {
  if (
    !value ||
    !value.startsWith('/') ||
    /^\/[/\\]/.test(value) ||
    [...value].some((c) => c < ' ' || c === '\x7f')
  )
    return '/';
  return value;
}

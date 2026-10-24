// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { useCallback, useEffect, useRef, useState } from 'react';
import { loadSites, Missing } from './sites-page-data.js';
import type { ListQuery } from './sites-page-query.js';
import type { SiteSummary } from './sites-page-sections.js';

export type MapState =
  | { kind: 'loading' }
  | { kind: 'error'; missing: boolean }
  | {
      kind: 'ready';
      sites: SiteSummary[];
      /** The IDs of the sites the filters match; undefined when unfiltered. */
      matches?: Set<string> | undefined;
    };

/**
 * Every site, and with filters on the IDs of those they match, each read in
 * full a page at a time; the server decides what matches, as on the list.
 * Both are read together for every query, so every match has a dot.
 * New filters keep the last picture until their answer arrives, and an
 * answer overtaken by a later request is dropped.
 */
export function useMapSites(
  api: ApiClient,
  query: Pick<ListQuery, 'g' | 't' | 'v' | 'q'>,
) {
  const [state, setState] = useState<MapState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const { g, t, v, q } = query;
  const asked = JSON.stringify([g, t, v, q.trim(), attempt]);
  const [answered, setAnswered] = useState<string | null>(null);
  const latest = useRef(0);

  useEffect(() => {
    const request = ++latest.current;
    const narrowed = Boolean(g || t || v || q.trim());
    Promise.all([
      loadSites(api, { q: '', sort: 'name' }),
      narrowed ? loadSites(api, { g, t, v, q, sort: 'name' }) : undefined,
    ]).then(
      ([sites, matching]) => {
        if (request !== latest.current) return;
        const known = new Set(sites.map((site) => site.id));
        setState({
          kind: 'ready',
          sites,
          matches:
            matching &&
            new Set(
              matching.map((site) => site.id).filter((id) => known.has(id)),
            ),
        });
        setAnswered(asked);
      },
      (error: unknown) => {
        if (request !== latest.current) return;
        setState({ kind: 'error', missing: error instanceof Missing });
        setAnswered(asked);
      },
    );
  }, [api, g, t, v, q, attempt, asked]);

  const retry = useCallback(() => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  }, []);
  return { state, pending: answered !== asked, retry };
}

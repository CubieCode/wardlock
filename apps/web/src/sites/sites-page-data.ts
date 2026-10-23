// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { Group, Tag } from './site-page-organise.js';
import type { ListQuery } from './sites-page-query.js';
import type { SiteSummary } from './sites-page-sections.js';

export type SiteCounts = components['schemas']['SiteCounts'];

export type ListState =
  | { kind: 'loading' }
  /** `missing`: the filter names a group or tag that is not the owner's. */
  | { kind: 'error'; missing: boolean }
  | { kind: 'ready'; sites: SiteSummary[] };

class Missing extends Error {}

/**
 * Every site matching the filters, read a page of 100 at a time in the
 * server's order. The server filters and sorts; the list groups.
 */
async function loadSites(
  api: ApiClient,
  { g, t, v, q, sort }: Pick<ListQuery, 'g' | 't' | 'v' | 'q' | 'sort'>,
) {
  const sites: SiteSummary[] = [];
  let cursor: string | undefined;
  do {
    const { data, response } = await api.GET('/api/v0/site-summaries', {
      params: {
        query: {
          limit: 100,
          sort,
          ...(g ? { group: g } : {}),
          ...(t ? { tag: t } : {}),
          ...(v ? { view: v } : {}),
          ...(q.trim() ? { q: q.trim() } : {}),
          ...(cursor ? { cursor } : {}),
        },
      },
    });
    if (!data) {
      if (response.status === 404) throw new Missing();
      throw new Error('could not list sites');
    }
    sites.push(...data.items);
    cursor = data.nextCursor;
  } while (cursor);
  return sites;
}

/**
 * The sites the list shows for these filters. A new filter keeps the last
 * list on screen until its answer arrives, and an answer overtaken by a
 * later request is dropped. `patch` changes the list in place, for showing a
 * change before `reload` reads it again.
 */
export function useSites(
  api: ApiClient,
  query: Pick<ListQuery, 'g' | 't' | 'v' | 'q' | 'sort'>,
) {
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const { g, t, v, q, sort } = query;
  // What the shown state answers; the list is pending until it answers the
  // filters asked for now.
  const asked = JSON.stringify([g, t, v, q, sort, attempt]);
  const [answered, setAnswered] = useState<string | null>(null);
  const latest = useRef(0);

  useEffect(() => {
    const request = ++latest.current;
    loadSites(api, { g, t, v, q, sort }).then(
      (sites) => {
        if (request !== latest.current) return;
        setState({ kind: 'ready', sites });
        setAnswered(asked);
      },
      (error: unknown) => {
        if (request !== latest.current) return;
        setState({ kind: 'error', missing: error instanceof Missing });
        setAnswered(asked);
      },
    );
  }, [api, g, t, v, q, sort, attempt, asked]);
  const pending = answered !== asked;

  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  const retry = useCallback(() => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  }, []);
  const patch = useCallback(
    (change: (sites: SiteSummary[]) => SiteSummary[]) =>
      setState((current) =>
        current.kind === 'ready'
          ? { kind: 'ready', sites: change(current.sites) }
          : current,
      ),
    [],
  );
  return { state, pending, reload, retry, patch };
}

export type MetaState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; groups: Group[]; tags: Tag[]; counts: SiteCounts };

/**
 * The owner's groups and tags (names, notes and colours for sections and
 * chips) and the site counts, read together and beside the list.
 */
export function useMeta(api: ApiClient) {
  const [state, setState] = useState<MetaState>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    Promise.all([
      api.GET('/api/v0/groups'),
      api.GET('/api/v0/tags'),
      api.GET('/api/v0/site-counts'),
    ]).then(
      ([groups, tags, counts]) => {
        if (!live) return;
        setState(
          groups.data && tags.data && counts.data
            ? {
                kind: 'ready',
                groups: groups.data.items,
                tags: tags.data.items,
                counts: counts.data,
              }
            : { kind: 'error' },
        );
      },
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);
  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  const retry = useCallback(() => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  }, []);
  return { state, reload, retry };
}

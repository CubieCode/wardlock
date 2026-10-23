// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { sitesHref } from '../shell/navigator.js';

export type SiteView = components['schemas']['SiteView'];
export type SiteSort = components['schemas']['SiteSort'];
export type GroupBy = 'group' | 'tag' | 'access' | 'none';
export type Density = 'comfortable' | 'compact';

/**
 * Everything the sites list shows, as its address keeps it: the filters
 * (`g`, `t`, `v`, `q`) the server applies, and how the list is drawn
 * (`by`, `sort`, `d`). Defaults stay out of the address.
 */
export interface ListQuery {
  /** A group's ID, or `none` for the ungrouped sites. */
  g?: string | undefined;
  /** A tag's ID. */
  t?: string | undefined;
  v?: SiteView | undefined;
  /** The search text as typed. */
  q: string;
  by: GroupBy;
  sort: SiteSort;
  d: Density;
}

const views: SiteView[] = [
  'pinned',
  'expiring',
  'public',
  'quiet',
  'untagged',
  'down',
];
const groupings: GroupBy[] = ['group', 'tag', 'access', 'none'];
const sorts: SiteSort[] = ['deploy', 'name', 'requests', 'links'];

const pick = <T extends string>(options: T[], value: string | null) =>
  options.find((option) => option === value);

/** The list's state from its address; unknown values fall back to defaults. */
export function readQuery(params: URLSearchParams): ListQuery {
  return {
    g: params.get('g') || undefined,
    t: params.get('t') || undefined,
    v: pick(views, params.get('v')),
    q: params.get('q') ?? '',
    by: pick(groupings, params.get('by')) ?? 'group',
    sort: pick(sorts, params.get('sort')) ?? 'deploy',
    d: params.get('d') === 'compact' ? 'compact' : 'comfortable',
  };
}

/** How the list is drawn, as address values with the defaults left out. */
export function viewParams({
  by,
  sort,
  d,
}: Pick<ListQuery, 'by' | 'sort' | 'd'>) {
  return {
    by: by === 'group' ? undefined : by,
    sort: sort === 'deploy' ? undefined : sort,
    d: d === 'compact' ? 'compact' : undefined,
  };
}

/** The address of the list in this state. */
export function queryHref(query: ListQuery) {
  return sitesHref({
    g: query.g,
    t: query.t,
    v: query.v,
    q: query.q.trim() ? query.q : undefined,
    ...viewParams(query),
  });
}

/** Whether anything narrows the list. */
export const filtered = (query: ListQuery) =>
  Boolean(query.g || query.t || query.v || query.q.trim());

/** The same view with every filter removed. */
export const unfiltered = (query: ListQuery): ListQuery => ({
  by: query.by,
  sort: query.sort,
  d: query.d,
  q: '',
});

/** A search word that is a token (SPEC "Search"), as a filter chip. */
export interface SearchToken {
  /** The word as written in the search. */
  word: string;
  label: string;
}

const isLabels: Record<string, string> = {
  public: 'Public',
  protected: 'Protected',
  down: 'Taken down',
  pinned: 'Pinned',
};

/**
 * The `tag:`, `group:` and `is:` words in a search, in order, split at any
 * run of white space as the server does. A word of just `tag:` with nothing
 * after the colon is free text, as on the server.
 */
export function searchTokens(q: string): SearchToken[] {
  const tokens: SearchToken[] = [];
  for (const word of q.split(/\s+/)) {
    const match = /^(tag|group|is):(.+)$/i.exec(word);
    if (!match) continue;
    const kind = match[1]!.toLowerCase();
    const value = match[2]!;
    const label =
      kind === 'tag'
        ? `Tag: ${value}`
        : kind === 'group'
          ? value.toLowerCase() === 'none'
            ? 'No group'
            : `Group: ${value}`
          : (isLabels[value.toLowerCase()] ?? `Is: ${value}`);
    tokens.push({ word, label });
  }
  return tokens;
}

/** The search without one of its words. */
export function withoutWord(q: string, word: string) {
  const words = q.split(/\s+/);
  const at = words.indexOf(word);
  if (at >= 0) words.splice(at, 1);
  return words.filter(Boolean).join(' ');
}

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import type { StackedBarsPoint } from '@wardlock/ui';

export type Analytics = components['schemas']['Analytics'];
export type AnalyticsDays = components['schemas']['AnalyticsDays'];
export type AccessVia = components['schemas']['AccessVia'];
export type BrowserFamily = components['schemas']['BrowserFamily'];
export type Group = components['schemas']['Group'];
export type Tag = components['schemas']['Tag'];
type SiteSummary = components['schemas']['SiteSummary'];

/** What the page counts: every site, or the sites of one group, tag or site. */
export type Scope =
  | { kind: 'all' }
  | { kind: 'group'; id: string }
  | { kind: 'tag'; id: string }
  | { kind: 'site'; name: string };

export const ranges: AnalyticsDays[] = [7, 30, 90];
const defaultDays: AnalyticsDays = 30;

/**
 * The scope and range in the page's address: `site`, `g` or `t` (the first
 * of them, in that order, if several are given) and `days`.
 */
export function readQuery(query: URLSearchParams): {
  scope: Scope;
  days: AnalyticsDays;
} {
  const site = query.get('site');
  const g = query.get('g');
  const t = query.get('t');
  const scope: Scope = site
    ? { kind: 'site', name: site }
    : g
      ? { kind: 'group', id: g }
      : t
        ? { kind: 'tag', id: t }
        : { kind: 'all' };
  const days = Number(query.get('days'));
  return {
    scope,
    days: ranges.includes(days as AnalyticsDays)
      ? (days as AnalyticsDays)
      : defaultDays,
  };
}

/** The analytics page's address for a scope and range. */
export function analyticsHref(scope: Scope, days: AnalyticsDays = defaultDays) {
  const query = new URLSearchParams();
  if (scope.kind === 'site') query.set('site', scope.name);
  if (scope.kind === 'group') query.set('g', scope.id);
  if (scope.kind === 'tag') query.set('t', scope.id);
  if (days !== defaultDays) query.set('days', String(days));
  const search = query.toString();
  return search ? `/analytics?${search}` : '/analytics';
}

/** What the totals row shows of the sites in scope, from their summaries. */
export interface SiteTotals {
  workingLinks: number;
  /** Available sites with no requests in 30 days. */
  quiet: number;
}

/** The owner's groups and tags, which name the scopes. */
export interface Organisation {
  groups: Group[];
  tags: Tag[];
}

/** The scope as one string, as the scope picker's option values hold it. */
export const scopeValue = (scope: Scope) =>
  scope.kind === 'group'
    ? `g:${scope.id}`
    : scope.kind === 'tag'
      ? `t:${scope.id}`
      : scope.kind === 'site'
        ? `s:${scope.name}`
        : '';

export function parseScope(value: string): Scope {
  const at = value.indexOf(':');
  const kind = value.slice(0, at);
  const id = value.slice(at + 1);
  if (kind === 'g') return { kind: 'group', id };
  if (kind === 't') return { kind: 'tag', id };
  if (kind === 's') return { kind: 'site', name: id };
  return { kind: 'all' };
}

export async function readOrganisation(api: ApiClient): Promise<Organisation> {
  const [groups, tags] = await Promise.all([
    api.GET('/api/v0/groups'),
    api.GET('/api/v0/tags'),
  ]);
  if (!groups.data || !tags.data) throw new Error('could not list groups');
  return { groups: groups.data.items, tags: tags.data.items };
}

/**
 * Working links and quiet sites among the sites in scope, from every summary,
 * a page of 100 at a time. Quiet sites leave out taken-down ones, as the
 * `quiet` saved view does.
 */
export async function readTotals(
  api: ApiClient,
  scope: Scope,
): Promise<SiteTotals> {
  const filter =
    scope.kind === 'group'
      ? { group: scope.id }
      : scope.kind === 'tag'
        ? { tag: scope.id }
        : scope.kind === 'site'
          ? { q: scope.name }
          : {};
  const sites: SiteSummary[] = [];
  let cursor: string | undefined;
  do {
    const { data } = await api.GET('/api/v0/site-summaries', {
      params: {
        query: {
          ...filter,
          limit: 100,
          sort: 'name',
          ...(cursor ? { cursor } : {}),
        },
      },
    });
    if (!data) throw new Error('could not list site summaries');
    sites.push(...data.items);
    cursor = data.nextCursor;
  } while (cursor);
  // Search also matches hostnames, tags and groups; keep only the site.
  const inScope =
    scope.kind === 'site'
      ? sites.filter((site) => site.name === scope.name)
      : sites;
  return {
    workingLinks: inScope.reduce((sum, site) => sum + site.workingLinks, 0),
    quiet: inScope.filter((site) => site.quiet && !site.unavailable).length,
  };
}

/**
 * Reads the requests for one scope and range. A missing or foreign site,
 * group or tag is `missing`; anything else that fails throws.
 */
export async function readAnalytics(
  api: ApiClient,
  scope: Scope,
  days: AnalyticsDays,
): Promise<Analytics | 'missing'> {
  const query =
    scope.kind === 'group'
      ? { days, group: scope.id }
      : scope.kind === 'tag'
        ? { days, tag: scope.id }
        : scope.kind === 'site'
          ? { days, site: scope.name }
          : { days };
  const { data, response } = await api.GET('/api/v0/analytics', {
    params: { query },
  });
  if (response.status === 404) return 'missing';
  if (!data) throw new Error('could not read analytics');
  return data;
}

const dayLabel = new Intl.DateTimeFormat('en-NZ', {
  weekday: 'short',
  day: 'numeric',
  month: 'short',
});
const dayTick = new Intl.DateTimeFormat('en-NZ', {
  day: 'numeric',
  month: 'short',
});

const localDay = (date: Date) =>
  `${date.getFullYear()}-${date.getMonth() + 1}-${date.getDate()}`;

/**
 * Allowed and refused requests on each day of the range, in the viewer's
 * timezone, oldest first. The range is whole UTC hours, so its first and
 * last local days can be part days; days with no requests count zero.
 */
export function requestsPerDay(analytics: Analytics): StackedBarsPoint[] {
  const points = new Map<string, StackedBarsPoint>();
  const day = new Date(analytics.from);
  day.setHours(0, 0, 0, 0);
  for (; day.getTime() < analytics.to; day.setDate(day.getDate() + 1)) {
    points.set(localDay(day), {
      key: localDay(day),
      label: dayLabel.format(day),
      tick: dayTick.format(day),
      values: [0, 0],
    });
  }
  for (const hour of analytics.hours) {
    const point = points.get(localDay(new Date(hour.hour)));
    if (!point) continue;
    point.values = [
      point.values[0] + hour.allowed,
      point.values[1] + hour.refused,
    ];
  }
  return [...points.values()];
}

/** Each way in as the page names it. Refused requests have none. */
export const viaNames: Record<Exclude<AccessVia, 'none'>, string> = {
  public: 'Public',
  link: 'Share link',
  password: 'Password',
  email: 'Email code',
  oauth: 'Signed in with a provider',
};

export const browserNames: Record<BrowserFamily, string> = {
  chrome: 'Chrome',
  safari: 'Safari',
  firefox: 'Firefox',
  edge: 'Edge',
  tool: 'Scripts and tools',
  other: 'Other browsers',
};

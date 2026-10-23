// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';

export type SiteSummary = components['schemas']['SiteSummary'];
export type SiteCounts = components['schemas']['SiteCounts'];
export type ExpiringLink = components['schemas']['ExpiringLink'];
export type Group = components['schemas']['Group'];

/** Expiring links listed one by one; the saved view has the rest. */
export const shownExpiringLinks = 5;
/** Pinned sites shown; the saved view has the rest. */
export const shownPinned = 8;
/** Live deploys shown. */
export const shownDeploys = 6;

export interface OverviewData {
  counts: SiteCounts;
  /** Every site, live deploy newest first. */
  sites: SiteSummary[];
  groups: Group[];
  expiring: ExpiringLink[];
}

/** Every site summary, newest live deploy first, a page at a time. */
async function allSummaries(api: ApiClient) {
  const sites: SiteSummary[] = [];
  let cursor: string | undefined;
  do {
    const { data } = await api.GET('/api/v0/site-summaries', {
      params: {
        query: { sort: 'deploy', limit: 100, ...(cursor ? { cursor } : {}) },
      },
    });
    if (!data) throw new Error('could not list site summaries');
    sites.push(...data.items);
    cursor = data.nextCursor;
  } while (cursor);
  return sites;
}

/**
 * What the overview shows, in four reads (and one more per hundred sites):
 * the counts, the summaries, the group names and the soonest expiring
 * links. Never one read per site.
 */
export async function loadOverview(api: ApiClient): Promise<OverviewData> {
  const [counts, sites, groups, expiring] = await Promise.all([
    api.GET('/api/v0/site-counts'),
    allSummaries(api),
    api.GET('/api/v0/groups'),
    api.GET('/api/v0/expiring-links', {
      params: { query: { limit: shownExpiringLinks } },
    }),
  ]);
  if (!counts.data || !groups.data || !expiring.data)
    throw new Error('could not load the overview');
  return {
    counts: counts.data,
    sites,
    groups: groups.data.items,
    expiring: expiring.data.items,
  };
}

/** Requests over the summary's 7 days. */
export const weekRequests = (site: SiteSummary) =>
  site.requests.reduce((sum, day) => sum + day, 0);

/** A group's sites, their requests over 7 days and their working links. */
export interface GroupRow {
  /** Undefined for the sites in no group. */
  group: Group | undefined;
  sites: number;
  requests: number;
  links: number;
}

/**
 * Each group in listGroups order, then No group when there are groups and
 * some sites are in none of them.
 */
export function groupRows({ counts, sites, groups }: OverviewData) {
  const totals = new Map<
    string | undefined,
    { requests: number; links: number }
  >();
  for (const site of sites) {
    const total = totals.get(site.groupId) ?? { requests: 0, links: 0 };
    total.requests += weekRequests(site);
    total.links += site.workingLinks;
    totals.set(site.groupId, total);
  }
  const sitesIn = new Map(counts.groups.map((g) => [g.groupId, g.sites]));
  const rows: GroupRow[] = groups.map((group) => ({
    group,
    sites: sitesIn.get(group.id) ?? 0,
    ...(totals.get(group.id) ?? { requests: 0, links: 0 }),
  }));
  if (rows.length > 0 && counts.noGroup > 0)
    rows.push({
      group: undefined,
      sites: counts.noGroup,
      ...(totals.get(undefined) ?? { requests: 0, links: 0 }),
    });
  return rows;
}

/** Working links that expire soon, over all sites. */
export const expiringTotal = (sites: SiteSummary[]) =>
  sites.reduce((sum, site) => sum + site.expiringLinks, 0);

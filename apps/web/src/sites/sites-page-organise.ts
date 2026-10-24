// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import type { SiteSummary } from './sites-page-sections.js';

export type OrganiseRequest = components['schemas']['OrganiseSitesRequest'];
export type SiteOrganisation = components['schemas']['SiteOrganisation'];

/** Adds one request per distinct `key` to `out`, naming its sites. */
function bucket(
  out: OrganiseRequest[],
  entries: {
    site: string;
    key: string;
    change: Omit<OrganiseRequest, 'sites'>;
  }[],
) {
  const by = new Map<string, OrganiseRequest>();
  for (const { site, key, change } of entries) {
    const request = by.get(key);
    if (request) request.sites.push(site);
    else by.set(key, { ...change, sites: [site] });
  }
  out.push(...by.values());
}

/**
 * The requests that put the sites back as `prior` says they were before
 * `change`: one per distinct prior state, and nothing for a site the change
 * left as it was. Each request changes something.
 */
export function inverse(
  change: OrganiseRequest,
  prior: SiteOrganisation[],
): OrganiseRequest[] {
  const out: OrganiseRequest[] = [];
  const add = change.addTagIds ?? [];
  const remove = change.removeTagIds ?? [];
  if (add.length)
    bucket(
      out,
      prior.flatMap((p) => {
        const lacked = add.filter((id) => !p.tagIds.includes(id));
        return lacked.length
          ? [
              {
                site: p.site,
                key: lacked.join(),
                change: { removeTagIds: lacked },
              },
            ]
          : [];
      }),
    );
  if (remove.length)
    bucket(
      out,
      prior.flatMap((p) => {
        const had = remove.filter((id) => p.tagIds.includes(id));
        return had.length
          ? [{ site: p.site, key: had.join(), change: { addTagIds: had } }]
          : [];
      }),
    );
  if (change.groupId !== undefined || change.ungroup)
    bucket(
      out,
      prior.flatMap((p) =>
        p.groupId === change.groupId
          ? []
          : [
              {
                site: p.site,
                key: p.groupId ?? '',
                change: p.groupId ? { groupId: p.groupId } : { ungroup: true },
              },
            ],
      ),
    );
  if (change.pinned !== undefined)
    bucket(
      out,
      prior.flatMap((p) =>
        p.pinned === change.pinned
          ? []
          : [
              // Re-pinned at its old time, so it goes back to its place.
              {
                site: p.site,
                key: `${p.pinned}:${p.pinnedAt ?? ''}`,
                change: {
                  pinned: p.pinned,
                  ...(p.pinnedAt !== undefined ? { pinnedAt: p.pinnedAt } : {}),
                },
              },
            ],
      ),
    );
  return out;
}

/**
 * The summaries as `change` leaves them, for showing a change before the
 * list is read again. `tagOrder` sorts tag IDs the way the server does (by
 * tag name).
 */
export function applyChange(
  sites: SiteSummary[],
  change: OrganiseRequest,
  tagOrder: (ids: string[]) => string[],
): SiteSummary[] {
  const names = new Set(change.sites);
  const add = change.addTagIds ?? [];
  const remove = new Set(change.removeTagIds ?? []);
  return sites.map((site) => {
    if (!names.has(site.name)) return site;
    const next = { ...site };
    if (add.length || remove.size)
      next.tagIds = tagOrder([
        ...new Set([...site.tagIds, ...add].filter((id) => !remove.has(id))),
      ]);
    if (change.groupId !== undefined) next.groupId = change.groupId;
    if (change.ungroup) delete next.groupId;
    if (change.pinned !== undefined) next.pinned = change.pinned;
    return next;
  });
}

/** Whether all, some or none of `sites` pass `test`. */
export function triState<T>(
  sites: T[],
  test: (site: T) => boolean,
): boolean | 'mixed' {
  let some = 0;
  for (const site of sites) if (test(site)) some += 1;
  if (some === 0) return false;
  return some === sites.length ? true : 'mixed';
}

/** "docs" for one site, "3 sites" for more. */
export const siteCount = (names: string[]) =>
  names.length === 1 ? names[0]! : `${names.length} sites`;

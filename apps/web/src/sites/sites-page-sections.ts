// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import { sitesHref, tagSlot } from '../shell/navigator.js';
import type { Group, Tag } from './site-page-organise.js';
import {
  viewParams,
  type GroupBy,
  type ListQuery,
} from './sites-page-query.js';

export type SiteSummary = components['schemas']['SiteSummary'];

/** One section of the list, on its own wire. */
export interface Section {
  /** Stable across reloads: what the section stands for. */
  key: string;
  title: string;
  /** The list filtered to what the section stands for, when there is one. */
  href?: string | undefined;
  /** A tag section's colour slot. */
  slot?: number | undefined;
  /** A group's note. */
  note?: string | undefined;
  /** In the order the server sorted them. */
  sites: SiteSummary[];
}

/** Whether a site counts as taken down, protected or public. */
export function access(site: SiteSummary) {
  if (site.unavailable) return 'down';
  return site.visibility;
}

/**
 * The list cut into sections: by group (in `listGroups` order, then No
 * group), by tag (a site with several tags under each of them, then
 * Untagged), by access, or one section named `allTitle`. Empty sections are
 * left out.
 */
export function buildSections(
  sites: SiteSummary[],
  by: GroupBy,
  groups: Group[],
  tags: Tag[],
  allTitle: string,
  /** How the list is drawn, kept by the sections' links. */
  view: Pick<ListQuery, 'by' | 'sort' | 'd'>,
): Section[] {
  const keep = viewParams(view);
  let sections: Section[];
  if (by === 'group') {
    const byGroup = new Map<string, SiteSummary[]>();
    for (const site of sites) {
      const key = site.groupId ?? '';
      const list = byGroup.get(key);
      if (list) list.push(site);
      else byGroup.set(key, [site]);
    }
    sections = groups.map((group) => ({
      key: `g:${group.id}`,
      title: group.name,
      href: sitesHref({ g: group.id, ...keep }),
      note: group.note,
      sites: byGroup.get(group.id) ?? [],
    }));
    sections.push({
      key: 'g:none',
      title: 'No group',
      href: sitesHref({ g: 'none', ...keep }),
      // A site whose group is not in the list (deleted meanwhile) is shown
      // here until the next reload.
      sites: sites.filter(
        (site) => !site.groupId || !groups.some((g) => g.id === site.groupId),
      ),
    });
  } else if (by === 'tag') {
    const byTag = new Map<string, SiteSummary[]>();
    for (const site of sites)
      for (const id of site.tagIds) {
        const list = byTag.get(id);
        if (list) list.push(site);
        else byTag.set(id, [site]);
      }
    sections = tags.map((tag) => ({
      key: `t:${tag.id}`,
      title: tag.name,
      href: sitesHref({ t: tag.id, ...keep }),
      slot: tagSlot(tag.colour),
      sites: byTag.get(tag.id) ?? [],
    }));
    sections.push({
      key: 't:none',
      title: 'Untagged',
      href: sitesHref({ v: 'untagged', ...keep }),
      sites: sites.filter((site) => site.tagIds.length === 0),
    });
  } else if (by === 'access') {
    sections = (
      [
        ['protected', 'Protected'],
        ['public', 'Public'],
        ['down', 'Taken down'],
      ] as const
    ).map(([kind, title]) => ({
      key: `a:${kind}`,
      title,
      sites: sites.filter((site) => access(site) === kind),
    }));
  } else {
    sections = [{ key: 'all', title: allTitle, sites }];
  }
  return sections.filter((section) => section.sites.length > 0);
}

/**
 * The rows a section shows: with more than one section, the first `cap`
 * until the owner asks for all of them.
 */
export function shownSites(
  section: Section,
  sectionCount: number,
  cap: number,
  expanded: boolean,
) {
  if (sectionCount < 2 || expanded || section.sites.length <= cap)
    return section.sites;
  return section.sites.slice(0, cap);
}

/** Requests over the 7 days a summary covers. */
export const weekRequests = (site: SiteSummary) =>
  site.requests.reduce((sum, day) => sum + day, 0);

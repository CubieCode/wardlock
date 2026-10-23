// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  CommandPalette,
  type CommandGroup,
  type CommandItem,
} from '@wardlock/ui';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { pageIcon, pinIcon, sitesIcon } from './icons.js';
import {
  navigatorFilters,
  savedViews,
  sitesHref,
  tagSlot,
} from './navigator.js';
import { navigate } from './router.js';
import { shellRoutes } from './routes.js';

type SiteSummary = components['schemas']['SiteSummary'];
type Group = components['schemas']['Group'];
type Tag = components['schemas']['Tag'];
type SiteCounts = components['schemas']['SiteCounts'];

/** How many sites jump-to shows for a search. */
const siteLimit = 8;
/** Waits this long after typing stops before searching. */
const searchDelay = 150;
/** The server's limit on a search (docs/SPEC.md, "Site summaries"). */
const maxQuery = 200;

type Sites =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; query: string; items: SiteSummary[] };

interface Filters {
  counts: SiteCounts;
  groups: Group[];
  tags: Tag[];
}

interface Target extends CommandItem {
  href: string;
}

/**
 * The jump-to palette over sites, pages and, once the sites list can show
 * them, saved views, groups and tags. Sites come from the server's search, so
 * every site is reachable however many there are; the rest is matched by
 * name here. Mount it only while open: it starts empty each time, and
 * unmounting returns focus to the opener.
 */
export function JumpTo({
  api,
  onClose,
  filters = navigatorFilters,
}: {
  api: ApiClient;
  onClose: () => void;
  filters?: boolean;
}) {
  const [query, setQuery] = useState('');
  const [sites, setSites] = useState<Sites>({ kind: 'loading' });
  const [named, setNamed] = useState<Filters>();
  const search = query.trim().slice(0, maxQuery);

  useEffect(() => {
    let live = true;
    const timer = setTimeout(
      () => {
        api
          .GET('/api/v0/site-summaries', {
            params: {
              query: {
                limit: siteLimit,
                // Recent deploys first until there is something to match.
                sort: search ? 'name' : 'deploy',
                ...(search ? { q: search } : {}),
              },
            },
          })
          .then(
            ({ data }) => {
              if (!live) return;
              setSites(
                data
                  ? { kind: 'ready', query: search, items: data.items }
                  : { kind: 'error' },
              );
            },
            () => live && setSites({ kind: 'error' }),
          );
      },
      search ? searchDelay : 0,
    );
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [api, search]);

  useEffect(() => {
    if (!filters) return;
    let live = true;
    Promise.all([
      api.GET('/api/v0/site-counts'),
      api.GET('/api/v0/groups'),
      api.GET('/api/v0/tags'),
    ]).then(
      ([counts, groups, tags]) => {
        if (live && counts.data && groups.data && tags.data)
          setNamed({
            counts: counts.data,
            groups: groups.data.items,
            tags: tags.data.items,
          });
      },
      () => {}, // Sites and pages still work without them.
    );
    return () => {
      live = false;
    };
  }, [api, filters]);

  const pending =
    sites.kind === 'loading' ||
    (sites.kind === 'ready' && sites.query !== search);
  // Nothing until the first answer (it is not delayed), so the highlight
  // starts on the first result rather than on a page the sites then push
  // down: the palette keeps the highlight on its option as results change.
  const groups = useMemo(
    () =>
      sites.kind === 'loading'
        ? []
        : results(search, sites, filters ? named : undefined),
    [search, sites, filters, named],
  );

  return (
    <CommandPalette
      open
      onClose={onClose}
      label="Jump to"
      placeholder={
        filters
          ? 'Jump to a site, view, group, tag or page'
          : 'Jump to a site or page'
      }
      query={query}
      onQueryChange={setQuery}
      groups={groups}
      onSelect={(item) => {
        onClose();
        navigate((item as Target).href);
      }}
      emptyText={pending ? 'Searching your sites…' : 'Nothing matches'}
      footer={
        sites.kind === 'error'
          ? 'Could not search your sites'
          : pending
            ? 'Searching…'
            : undefined
      }
    />
  );
}

function icon(glyph: ReactNode) {
  return <span className="shell-palette-icon">{glyph}</span>;
}

/** The palette's groups for a search, in the order they are shown. */
export function results(
  search: string,
  sites: Sites,
  named: Filters | undefined,
): CommandGroup[] {
  const words = search.toLowerCase();
  const matches = (label: string) =>
    !words || label.toLowerCase().includes(words);

  // Only results for the current search: older ones would jump on Enter.
  const siteItems: Target[] =
    sites.kind === 'ready' && sites.query === search
      ? sites.items.map((site) => ({
          id: `site:${site.id}`,
          label: site.name,
          hint: site.hostname,
          icon: icon(site.pinned ? pinIcon : sitesIcon),
          href: `/sites/${encodeURIComponent(site.name)}`,
        }))
      : [];

  const out: { label: string; items: Target[] }[] = [
    { label: 'Sites', items: siteItems },
  ];
  if (named) {
    out.push(
      {
        label: 'Views',
        items: savedViews
          .filter(({ label }) => matches(label))
          .map(({ view, label, icon: glyph }) => ({
            id: `view:${view}`,
            label,
            hint: sitesCount(named.counts.views[view]),
            icon: icon(glyph),
            href: sitesHref({ v: view }),
          })),
      },
      {
        label: 'Groups',
        items: named.groups
          .filter((group) => matches(group.name))
          .map((group) => ({
            id: `group:${group.id}`,
            label: group.name,
            hint: sitesCount(
              named.counts.groups.find((row) => row.groupId === group.id)
                ?.sites ?? 0,
            ),
            icon: icon(<span className="shell-nav-hub" />),
            href: sitesHref({ g: group.id }),
          })),
      },
      {
        label: 'Tags',
        items: named.tags
          .filter((tag) => matches(tag.name))
          .map((tag) => ({
            id: `tag:${tag.id}`,
            label: tag.name,
            hint: sitesCount(
              named.counts.tags.find((row) => row.tagId === tag.id)?.sites ?? 0,
            ),
            icon: icon(
              <span
                className="wl-tag-dot"
                style={{
                  background: `var(--color-tag-${tagSlot(tag.colour)})`,
                }}
              />,
            ),
            href: sitesHref({ t: tag.id }),
          })),
      },
    );
  }
  out.push({
    label: 'Pages',
    items: shellRoutes.flatMap((route) =>
      route.rail && matches(route.rail.label)
        ? [
            {
              id: `page:${route.path}`,
              label: route.rail.label,
              icon: icon(pageIcon),
              href: route.path,
            },
          ]
        : [],
    ),
  });
  return out;
}

function sitesCount(n: number) {
  return n === 1 ? '1 site' : `${n} sites`;
}

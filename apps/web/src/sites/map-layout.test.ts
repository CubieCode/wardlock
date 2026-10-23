// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import {
  dotCap,
  dotRadius,
  layoutMap,
  maxDot,
  minDot,
  type MapLayout,
} from './map-layout.js';
import type { Group } from './site-page-organise.js';
import type { SiteSummary } from './sites-page-sections.js';

const id = (n: number) =>
  `01928c5e-7a1b-7c3d-9e4f-${String(n).padStart(12, '0')}`;

let serial = 0;
function site(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  serial += 1;
  return {
    id: id(serial),
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: 1767225600000 + serial * 1000,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'public',
    methods: [],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [0, 0, 0, 0, 0, 0, 0],
    quiet: true,
    ...extra,
  };
}

const docs: Group = { id: id(9001), name: 'Docs', createdAt: 0 };
const work: Group = { id: id(9002), name: 'Client work', createdAt: 0 };
const empty: Group = { id: id(9003), name: 'Empty', createdAt: 0 };

/** `n` sites spread over the groups, every third protected. */
function many(n: number, groups: Group[]) {
  return Array.from({ length: n }, (_, i) =>
    site(`site-${i}`, {
      ...(i % (groups.length + 1)
        ? { groupId: groups[i % groups.length]!.id }
        : {}),
      visibility: i % 3 ? 'public' : 'protected',
      requests: [i, 0, 0, 0, 0, 0, 0],
    }),
  );
}

const dots = (layout: MapLayout) => layout.hubs.flatMap((hub) => hub.dots);

/** Every pair of drawn dots in one hub is at least `gap` apart, edge to edge. */
function apart(layout: MapLayout, gap = 1) {
  for (const hub of layout.hubs)
    for (const [i, a] of hub.dots.entries())
      for (const b of hub.dots.slice(i + 1))
        if (Math.hypot(a.x - b.x, a.y - b.y) < a.r + b.r + gap) return false;
  return true;
}

/** No two clusters overlap. */
function clustersApart(layout: MapLayout) {
  const extent = layout.hubs.map((hub) => ({
    hub,
    r: Math.max(
      22,
      ...hub.dots.map((d) => Math.hypot(d.x - hub.x, d.y - hub.y) + d.r),
    ),
  }));
  return extent.every((a, i) =>
    extent
      .slice(i + 1)
      .every(
        (b) => Math.hypot(a.hub.x - b.hub.x, a.hub.y - b.hub.y) > a.r + b.r,
      ),
  );
}

describe('map layout', () => {
  it('has one hub per group in order, then No group when it has sites', () => {
    const sites = [
      site('alpha', { groupId: work.id }),
      site('beta'),
      site('gamma', { groupId: docs.id }),
    ];
    const layout = layoutMap(sites, [docs, work, empty]);
    expect(layout.hubs.map((hub) => hub.title)).toEqual([
      'Docs',
      'Client work',
      'Empty',
      'No group',
    ]);
    expect(layout.hubs.map((hub) => hub.group)).toEqual([
      docs.id,
      work.id,
      empty.id,
      'none',
    ]);
    expect(
      layoutMap([site('only', { groupId: docs.id })], [docs]).hubs.map(
        (hub) => hub.title,
      ),
    ).toEqual(['Docs']);
  });

  it('files a site whose group is gone under No group', () => {
    const layout = layoutMap([site('orphan', { groupId: id(1) })], [docs]);
    expect(layout.hubs.at(-1)!.title).toBe('No group');
    expect(layout.hubs.at(-1)!.sites.map((s) => s.name)).toEqual(['orphan']);
  });

  it('puts a ward only on wires to groups with protected sites', () => {
    const layout = layoutMap(
      [
        site('locked', { groupId: docs.id, visibility: 'protected' }),
        site('open', { groupId: work.id }),
        // Taken down counts as taken down, not protected.
        site('down', {
          groupId: empty.id,
          visibility: 'protected',
          unavailable: true,
        }),
      ],
      [docs, work, empty],
    );
    expect(layout.hubs.map((hub) => Boolean(hub.ward))).toEqual([
      true,
      false,
      false,
    ]);
    expect(dots(layout).map((dot) => dot.access)).toEqual([
      'protected',
      'public',
      'down',
    ]);
  });

  it('sizes dots by 7-day requests, capped', () => {
    expect(dotRadius(0, 100)).toBe(minDot);
    expect(dotRadius(100, 100)).toBe(maxDot);
    expect(dotRadius(25, 100)).toBeCloseTo(minDot + (maxDot - minDot) / 2);
    expect(dotRadius(5, 0)).toBe(minDot);
    const layout = layoutMap(
      [
        site('quiet'),
        site('busy', { requests: [1000, 1000, 0, 0, 0, 0, 0] }),
        site('some', { requests: [500, 0, 0, 0, 0, 0, 0] }),
      ],
      [],
    );
    expect(layout.maxRequests).toBe(2000);
    const r = Object.fromEntries(dots(layout).map((d) => [d.site.name, d.r]));
    expect(r.quiet).toBe(minDot);
    expect(r.busy).toBe(maxDot);
    expect(r.some).toBeGreaterThan(minDot);
    expect(r.some).toBeLessThan(maxDot);
  });

  it('draws the same picture from the same data, in any order', () => {
    const sites = many(40, [docs, work]);
    const a = layoutMap(sites, [docs, work]);
    const b = layoutMap(sites.toReversed(), [docs, work]);
    expect(b).toEqual(a);
  });

  it('keeps every dot in place when a newer site is added', () => {
    const sites = many(20, [docs]);
    const before = layoutMap(sites, [docs]);
    const after = layoutMap(
      [...sites, site('newest', { groupId: docs.id })],
      [docs],
    );
    const place = (layout: MapLayout) =>
      Object.fromEntries(
        layout.hubs.flatMap((hub) =>
          hub.dots.map((d) => [d.site.id, [d.x - hub.x, d.y - hub.y]]),
        ),
      );
    const old = place(before);
    const now = place(after);
    for (const [key, [x, y]] of Object.entries(old)) {
      expect(now[key]![0]).toBeCloseTo(x!, 0);
      expect(now[key]![1]).toBeCloseTo(y!, 0);
    }
  });

  it('keeps dots and clusters apart at 60 and at 300 sites', () => {
    const groups = [docs, work, empty];
    for (const n of [60, 300]) {
      const layout = layoutMap(many(n, groups), groups);
      expect(apart(layout)).toBe(true);
      expect(clustersApart(layout)).toBe(true);
      for (const dot of dots(layout)) {
        expect(dot.x).toBeGreaterThan(0);
        expect(dot.y).toBeGreaterThan(0);
        expect(dot.x).toBeLessThan(layout.width);
        expect(dot.y).toBeLessThan(layout.height);
      }
    }
  });

  it('draws at most the cap per hub, pinned, matching and busiest first', () => {
    const sites = Array.from({ length: dotCap + 20 }, (_, i) =>
      site(`s-${String(i).padStart(3, '0')}`, {
        requests: [i, 0, 0, 0, 0, 0, 0],
        pinned: i === 0,
      }),
    );
    const quiet = sites[1]!;
    const layout = layoutMap(sites, [], new Set([quiet.id]));
    const [hub] = layout.hubs;
    expect(hub!.dots).toHaveLength(dotCap);
    expect(hub!.hidden).toBe(20);
    const drawn = new Set(hub!.dots.map((d) => d.site.name));
    // Pinned and matching sites are drawn, though both are among the quietest.
    expect(drawn.has('s-000')).toBe(true);
    expect(drawn.has('s-001')).toBe(true);
    // Then the busiest: the quietest of the rest are left out.
    expect(drawn.has('s-002')).toBe(false);
    expect(drawn.has(`s-0${dotCap + 19}`)).toBe(true);
    expect(hub!.sites).toHaveLength(dotCap + 20);
  });

  it('dims sites outside the filters, and hubs with none inside', () => {
    const sites = [
      site('match', { groupId: docs.id }),
      site('miss', { groupId: docs.id }),
      site('other', { groupId: work.id }),
    ];
    const unfiltered = layoutMap(sites, [docs, work]);
    expect(dots(unfiltered).some((d) => d.dim)).toBe(false);
    expect(unfiltered.hubs.map((hub) => hub.matching)).toEqual([2, 1]);

    const layout = layoutMap(sites, [docs, work], new Set([sites[0]!.id]));
    expect(
      Object.fromEntries(dots(layout).map((d) => [d.site.name, d.dim])),
    ).toEqual({ match: false, miss: true, other: true });
    expect(layout.hubs.map((hub) => [hub.dim, hub.matching])).toEqual([
      [false, 1],
      [true, 0],
    ]);
  });
});

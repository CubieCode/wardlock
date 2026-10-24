// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { Group } from './site-page-organise.js';
import {
  access,
  weekRequests,
  type SiteSummary,
} from './sites-page-sections.js';

// The map is laid out in CSS pixels for the width it is drawn at and never
// scaled, so its text is always the size the stylesheet gives it.

/** The server node's radius. */
export const serverRadius = 44;
/** The most dots one hub draws; the rest are in the text list. */
export const dotCap = 60;
/** Below this width the map uses smaller hubs, dots and spacing. */
export const compactWidth = 600;
/** The width laid out for before the map is measured. */
export const defaultWidth = 960;

/** Sizes for one density: hub radius, dot radii and spiral spacing. */
export interface MapMetrics {
  hub: number;
  minDot: number;
  maxDot: number;
  step: number;
}

const roomy: MapMetrics = { hub: 22, minDot: 4, maxDot: 11, step: 14 };
const compact: MapMetrics = { hub: 18, minDot: 3.5, maxDot: 8, step: 10.5 };

/** The roomy sizes, which the legend's size key also uses. */
export const { minDot, maxDot } = roomy;

/** Text sizes in CSS pixels, as map.css sets them. */
export const titleSize = 14;
export const labelSize = 12;
// Rough advance of a monospace character, for fitting titles and labels.
const charWidth = (size: number) => size * 0.62;
const labelChars = 24;

// Sites spiral out from their hub on a sunflower (Vogel) pattern: the k-th
// drawn site sits at a fixed angle and distance, so a new site adds a dot at
// the edge and never moves the others.
const golden = Math.PI * (3 - Math.sqrt(5));
// Room between clusters and around the picture.
const gap = 20;
const margin = 16;

export type Access = 'protected' | 'public' | 'down';

/** A pinned site's name beside its dot, on the side away from the hub. */
export interface MapLabel {
  x: number;
  y: number;
  anchor: 'start' | 'middle' | 'end';
  text: string;
}

export interface MapDot {
  site: SiteSummary;
  x: number;
  y: number;
  r: number;
  access: Access;
  requests: number;
  /** Filters are on and the site does not match them. */
  dim: boolean;
  /** Left out when it would cover another label or the hub. */
  label?: MapLabel | undefined;
}

export interface MapHub {
  /** `g:<id>`, or `g:none` for the ungrouped sites. */
  key: string;
  /** The group's ID, or `none`. */
  group: string;
  title: string;
  x: number;
  y: number;
  r: number;
  /** Every site in the group, in placement order. */
  sites: SiteSummary[];
  /** The drawn sites: all of them, or `dotCap` when there are more. */
  dots: MapDot[];
  /** How many of the group's sites have no dot. */
  hidden: number;
  /** How many of those match the filters. */
  hiddenMatching: number;
  /** How many match the filters (all of them when there are none). */
  matching: number;
  protectedSites: number;
  requests: number;
  /** The group has protected sites: a ward sits on its wire. */
  ward?: { x: number; y: number } | undefined;
  /** The wire from the server to the hub, as points. */
  wire: { x: number; y: number }[];
  /** The title as drawn, shortened to fit its cluster. */
  titleText: string;
  titleAt: { x: number; y: number };
  dim: boolean;
}

export interface MapLayout {
  width: number;
  height: number;
  metrics: MapMetrics;
  server: { x: number; y: number };
  hubs: MapHub[];
  /** The 7-day request count the largest dot stands for, and above. */
  maxRequests: number;
  sites: number;
}

/** A dot's radius: area grows with requests, capped at `maxRequests`. */
export function dotRadius(
  requests: number,
  maxRequests: number,
  { minDot, maxDot }: Pick<MapMetrics, 'minDot' | 'maxDot'> = roomy,
) {
  if (maxRequests <= 0 || requests <= 0) return minDot;
  return (
    minDot +
    (maxDot - minDot) * Math.sqrt(Math.min(requests, maxRequests) / maxRequests)
  );
}

/**
 * The count the largest dot stands for: the 90th percentile of the sites
 * with requests, so one outlier does not shrink every other dot.
 */
export function sizeCap(sites: SiteSummary[]) {
  const counts = sites
    .map(weekRequests)
    .filter((n) => n > 0)
    .toSorted((a, b) => a - b);
  if (!counts.length) return 0;
  return counts[Math.ceil(counts.length * 0.9) - 1]!;
}

const byName = (a: SiteSummary, b: SiteSummary) =>
  a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
const byAge = (a: SiteSummary, b: SiteSummary) =>
  a.createdAt - b.createdAt || byName(a, b);

/**
 * The sites a hub draws when it has more than `cap`: pinned first, then the
 * busiest, then the oldest. The filters never change the choice, so
 * filtering only fades dots and never moves one. Drawn in placement order.
 */
function drawnSites(sites: SiteSummary[], cap: number) {
  if (sites.length <= cap) return sites;
  const chosen = new Set(
    sites
      .map((site) => ({ site, requests: weekRequests(site) }))
      .toSorted(
        (a, b) =>
          Number(b.site.pinned) - Number(a.site.pinned) ||
          b.requests - a.requests ||
          byAge(a.site, b.site),
      )
      .slice(0, cap)
      .map(({ site }) => site),
  );
  return sites.filter((site) => chosen.has(site));
}

interface Box {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
}

const overlap = (a: Box, b: Box, pad = 0) =>
  a.x1 < b.x2 + pad &&
  b.x1 < a.x2 + pad &&
  a.y1 < b.y2 + pad &&
  b.y1 < a.y2 + pad;
const move = (b: Box, x: number, y: number): Box => ({
  x1: b.x1 + x,
  y1: b.y1 + y,
  x2: b.x2 + x,
  y2: b.y2 + y,
});
const union = (a: Box, b: Box): Box => ({
  x1: Math.min(a.x1, b.x1),
  y1: Math.min(a.y1, b.y1),
  x2: Math.max(a.x2, b.x2),
  y2: Math.max(a.y2, b.y2),
});

/** How far point (px, py) is from the segment from the origin to (x, y). */
function fromWire(px: number, py: number, x: number, y: number) {
  const t = Math.max(0, Math.min(1, (px * x + py * y) / (x * x + y * y || 1)));
  return Math.hypot(px - t * x, py - t * y);
}

const shorten = (text: string, chars: number) =>
  text.length > chars ? `${text.slice(0, Math.max(1, chars - 1))}…` : text;

/** A label beside a dot at (dx, dy) from its hub, pointing away from it. */
function placeLabel(dx: number, dy: number, r: number, name: string) {
  const away = Math.hypot(dx, dy) || 1;
  const ux = dx / away;
  const uy = dy / away;
  const text = shorten(name, labelChars);
  const width = text.length * charWidth(labelSize);
  const anchor: MapLabel['anchor'] =
    ux > 0.38 ? 'start' : ux < -0.38 ? 'end' : 'middle';
  const x = dx + ux * (r + 4);
  const y =
    anchor !== 'middle'
      ? dy + uy * (r + 4) + labelSize * 0.35
      : uy < 0
        ? dy - r - 4
        : dy + r + 4 + labelSize * 0.8;
  const x1 =
    anchor === 'start' ? x : anchor === 'end' ? x - width : x - width / 2;
  const label: MapLabel = { x, y, anchor, text };
  const box: Box = {
    x1,
    y1: y - labelSize * 0.8,
    x2: x1 + width,
    y2: y + labelSize * 0.25,
  };
  return { label, box };
}

/**
 * One cluster before placement: its dots and labels relative to the hub,
 * its radius, and the room it needs with its title above or below. Labels
 * and the title stay within `half` of the hub either side, so a cluster
 * fits the picture's width.
 */
function shapeCluster(
  drawn: SiteSummary[],
  title: string,
  metrics: MapMetrics,
  maxRequests: number,
  half: number,
) {
  const firstRing = metrics.hub + metrics.maxDot + 8;
  const dots = drawn.map((site, k) => {
    const theta = -Math.PI / 2 + k * golden;
    const rho = firstRing + metrics.step * Math.sqrt(k);
    const requests = weekRequests(site);
    return {
      site,
      dx: Math.cos(theta) * rho,
      dy: Math.sin(theta) * rho,
      r: dotRadius(requests, maxRequests, metrics),
      requests,
    };
  });
  const r = dots.length
    ? Math.max(...dots.map((d) => Math.hypot(d.dx, d.dy) + d.r))
    : metrics.hub + 6;
  let body: Box = { x1: -r, y1: -r, x2: r, y2: r };
  // Labels go oldest first; one that would cover the hub or an earlier
  // label, or reach past the width, is left out (its name stays in the
  // dot's tooltip and name).
  const hubBox: Box = {
    x1: -metrics.hub,
    y1: -metrics.hub,
    x2: metrics.hub,
    y2: metrics.hub,
  };
  const taken: Box[] = [hubBox];
  const labels = new Map<string, MapLabel>();
  for (const dot of dots) {
    if (!dot.site.pinned) continue;
    const { label, box } = placeLabel(dot.dx, dot.dy, dot.r, dot.site.name);
    if (
      box.x1 < -half ||
      box.x2 > half ||
      taken.some((t) => overlap(t, box, 2))
    )
      continue;
    taken.push(box);
    labels.set(dot.site.id, label);
    body = union(body, box);
  }
  const titleText = shorten(
    title,
    Math.floor(
      Math.min(Math.max(2 * r + 40, 168), 2 * half) / charWidth(titleSize),
    ),
  );
  const tw = titleText.length * charWidth(titleSize);
  const above = {
    at: { x: 0, y: body.y1 - 8 },
    box: union(body, {
      x1: -tw / 2,
      y1: body.y1 - 8 - titleSize,
      x2: tw / 2,
      y2: body.y1,
    }),
  };
  const below = {
    at: { x: 0, y: body.y2 + 6 + titleSize },
    box: union(body, {
      x1: -tw / 2,
      y1: body.y2,
      x2: tw / 2,
      y2: body.y2 + 10 + titleSize,
    }),
  };
  return { dots, labels, r, titleText, above, below };
}

type Shape = ReturnType<typeof shapeCluster>;

interface Spot {
  x: number;
  y: number;
  r: number;
  box: Box;
  up: boolean;
  /** In the tree, the height of the bus the hub's wire drops from. */
  bus?: number;
}

const serverBox: Box = {
  x1: -serverRadius - gap,
  y1: -serverRadius - gap,
  x2: serverRadius + gap,
  y2: serverRadius + gap,
};

/** A cluster at (x, y), its title on the side away from the server. */
function spotAt(shape: Shape, x: number, y: number): Spot {
  const up = y <= 0;
  return {
    x,
    y,
    r: shape.r,
    box: move((up ? shape.above : shape.below).box, x, y),
    up,
  };
}

/**
 * Within the width, clear of the server, of the clusters in `others` and of
 * their wires, and its own wire clear of them.
 */
function clear(spot: Spot, others: Spot[], half: number) {
  const { box } = spot;
  if (
    box.x2 - box.x1 <= 2 * half &&
    (box.x1 < -half - 0.5 || box.x2 > half + 0.5)
  )
    return false;
  if (overlap(box, serverBox)) return false;
  return others.every(
    (other) =>
      !overlap(box, other.box, gap) &&
      fromWire(other.x, other.y, spot.x, spot.y) >= other.r + 6 &&
      fromWire(spot.x, spot.y, other.x, other.y) >= spot.r + 6,
  );
}

/** Whether every cluster is clear of the others and of their wires. */
const allClear = (spots: Spot[], half: number) =>
  spots.every((spot, i) => clear(spot, spots.slice(i + 1), half));

/** A footprint's reach from its hub, for spacing clusters on a ring. */
const reach = (shape: Shape) => {
  const { box } = shape.above;
  return Math.max(-box.x1, box.x2, -box.y1, box.y2);
};

/** An angle for each share of a whole turn, starting at the top. */
function shareAngles(sizes: number[], from = -Math.PI / 2) {
  const total = sizes.reduce((sum, n) => sum + n, 0) || 1;
  let before = 0;
  return sizes.map((size) => {
    const angle = from + (2 * Math.PI * (before + size / 2)) / total;
    before += size;
    return angle;
  });
}

/**
 * Hubs on rings around the server: one ring, or two with each outer wire
 * running through a gap between the inner clusters. Each ring is an
 * ellipse no wider than the picture; it grows until every cluster is clear
 * of the others and of every wire. The shortest clean picture wins; none
 * means the rings could not be made clean within reason.
 */
function placeRings(shapes: Shape[], half: number, wide: boolean) {
  const n = shapes.length;
  const reaches = shapes.map(reach);
  const largest = Math.max(0, ...reaches);
  const stretch = wide ? 1.4 : 1;
  const start = serverRadius + gap + largest;
  const candidates: Spot[][] = [];
  const onRing = (shape: Shape, angle: number, rx: number, ry: number) => {
    const { box } = shape.above;
    const x = Math.min(
      Math.max(Math.cos(angle) * rx, -half - box.x1),
      half - box.x2,
    );
    return spotAt(shape, x, Math.sin(angle) * ry);
  };

  // One ring.
  const angles = shareAngles(reaches.map((r) => 2 * r + gap));
  for (let ry = start; ry < start * 12; ry *= 1.04) {
    const spots = shapes.map((shape, i) =>
      onRing(shape, angles[i]!, ry * stretch, ry),
    );
    if (allClear(spots, half)) {
      candidates.push(spots);
      break;
    }
  }

  // Two rings: the first half inside, the rest outside, each outer cluster
  // facing a gap between two inner ones.
  if (n >= 4) {
    const m = Math.ceil(n / 2);
    const inner = shapes.slice(0, m);
    const outer = shapes.slice(m);
    const innerReach = Math.max(...reaches.slice(0, m));
    const outerReach = Math.max(...reaches.slice(m));
    const corridor = 2 * (gap + 6);
    const innerAngles = shareAngles(
      reaches.slice(0, m).map((r) => 2 * r + gap + corridor),
    );
    // The gap after inner cluster j, spread evenly over the outer ones.
    const gapAngle = (j: number) => {
      const a = innerAngles[j]!;
      let b = innerAngles[(j + 1) % m]!;
      if (b <= a) b += 2 * Math.PI;
      return (a + b) / 2;
    };
    const outerAngles = outer.map((_, k) =>
      gapAngle(Math.floor((k * m) / outer.length)),
    );
    const between = innerReach + outerReach + gap;
    const innerStart = serverRadius + gap + innerReach;
    for (let ry = innerStart; ry < innerStart * 12; ry *= 1.04) {
      const spots = [
        ...inner.map((shape, j) =>
          onRing(shape, innerAngles[j]!, ry * stretch, ry),
        ),
        ...outer.map((shape, k) =>
          onRing(
            shape,
            outerAngles[k]!,
            (ry + between) * stretch,
            ry + between,
          ),
        ),
      ];
      if (allClear(spots, half)) {
        candidates.push(spots);
        break;
      }
    }
  }
  return candidates.toSorted((a, b) => height(a) - height(b))[0];
}

/** Where the tree's trunk runs, from the picture's left. */
const trunkInset = 6;
/** The corridor above each row of the tree, which its bus runs along. */
const busGap = 36;

/**
 * The tree, for narrow pictures and for groups too many for clean rings:
 * the server at the top, a trunk down the left edge, and rows of clusters
 * (titles below) filling the width, each row under a bus that drops a wire
 * to each of its hubs. No wire can cross a cluster.
 */
function treeClusters(shapes: Shape[], half: number) {
  const left = -half + trunkInset + 14;
  const spots: Spot[] = [];
  let cursor = serverRadius;
  let i = 0;
  while (i < shapes.length) {
    const row: Shape[] = [];
    let used = 0;
    while (i < shapes.length) {
      const { box } = shapes[i]!.below;
      const w = box.x2 - box.x1;
      if (row.length && used + gap + w > half - left) break;
      used += (row.length ? gap : 0) + w;
      row.push(shapes[i]!);
      i++;
    }
    const top = cursor + busGap;
    let x = left + Math.max(0, (half - left - used) / 2);
    let bottom = top;
    for (const shape of row) {
      const { box } = shape.below;
      const hub = { x: x - box.x1, y: top - box.y1 };
      spots.push({
        ...hub,
        r: shape.r,
        box: move(box, hub.x, hub.y),
        up: false,
        bus: top - busGap / 2,
      });
      bottom = Math.max(bottom, hub.y + box.y2);
      x += box.x2 - box.x1 + gap;
    }
    cursor = bottom;
  }
  return spots;
}

const height = (spots: Spot[]) =>
  Math.max(serverRadius, ...spots.map((s) => s.box.y2)) -
  Math.min(-serverRadius, ...spots.map((s) => s.box.y1));

const round = (n: number) => Math.round(n * 10) / 10;

/**
 * The map for a picture `width` pixels wide: the server near the middle,
 * one hub per group in `groups` order and then No group (when it has
 * sites), each with its sites around it. Pure and deterministic: the same
 * sites, groups and width always give the same picture. `matches` holds the
 * IDs of the sites the filters match; leave it out when nothing is
 * filtered. Filters only fade: they never change which dots are drawn or
 * where.
 */
export function layoutMap(
  sites: SiteSummary[],
  groups: Group[],
  matches?: Set<string>,
  { width = defaultWidth, cap = dotCap } = {},
): MapLayout {
  const metrics = width < compactWidth ? compact : roomy;
  const known = new Set(groups.map((group) => group.id));
  const ordered = sites.toSorted(byAge);
  const members = new Map<string, SiteSummary[]>();
  for (const site of ordered) {
    // A site whose group is not in the list (deleted meanwhile) is shown
    // under No group, as on the list.
    const key = site.groupId && known.has(site.groupId) ? site.groupId : 'none';
    const list = members.get(key);
    if (list) list.push(site);
    else members.set(key, [site]);
  }
  const maxRequests = sizeCap(sites);
  const half = width / 2 - margin;
  const clusters = [
    ...groups.map((group) => ({
      group: group.id,
      title: group.name,
      sites: members.get(group.id) ?? [],
    })),
    ...(members.has('none')
      ? [{ group: 'none', title: 'No group', sites: members.get('none')! }]
      : []),
  ].map((cluster) => {
    const drawn = drawnSites(cluster.sites, cap);
    return {
      ...cluster,
      drawn,
      // Room for the tree's trunk on the left.
      shape: shapeCluster(
        drawn,
        cluster.title,
        metrics,
        maxRequests,
        half - trunkInset - 7,
      ),
    };
  });

  // Rings around the server while they come out clean and no taller than
  // they are wide; otherwise, and on narrow pictures, the tree.
  const shapes = clusters.map((c) => c.shape);
  const rings =
    metrics === compact ? undefined : placeRings(shapes, half, width >= 900);
  const spots =
    rings && height(rings) <= width ? rings : treeClusters(shapes, half);
  const trunk = -half + trunkInset;

  const filtering = matches !== undefined;
  const hubs = clusters.map((cluster, i): MapHub => {
    const { x, y, up, bus } = spots[i]!;
    const { shape } = cluster;
    const away = Math.hypot(x, y) || 1;
    const ux = x / away;
    const uy = y / away;
    const dots = shape.dots.map((dot): MapDot => ({
      site: dot.site,
      x: x + dot.dx,
      y: y + dot.dy,
      r: dot.r,
      access: access(dot.site),
      requests: dot.requests,
      dim: filtering && !matches.has(dot.site.id),
      label: ((label) =>
        label ? { ...label, x: x + label.x, y: y + label.y } : undefined)(
        shape.labels.get(dot.site.id),
      ),
    }));
    const drawn = new Set(cluster.drawn);
    const isMatch = (site: SiteSummary) => !filtering || matches.has(site.id);
    const protectedSites = cluster.sites.filter(
      (site) => access(site) === 'protected',
    ).length;
    // The ward sits between the server and the near edge of the cluster,
    // or in the tree, on the hub's drop below the bus.
    const wardAt = (serverRadius + away - shape.r) / 2;
    const ward =
      bus === undefined
        ? { x: ux * wardAt, y: uy * wardAt }
        : { x, y: bus + busGap / 4 };
    const title = up ? shape.above.at : shape.below.at;
    const matching = cluster.sites.filter(isMatch).length;
    return {
      key: `g:${cluster.group}`,
      group: cluster.group,
      title: cluster.title,
      x,
      y,
      r: shape.r,
      sites: cluster.sites,
      dots,
      hidden: cluster.sites.length - dots.length,
      hiddenMatching: cluster.sites.filter(
        (site) => !drawn.has(site) && isMatch(site),
      ).length,
      matching,
      protectedSites,
      requests: cluster.sites.reduce(
        (sum, site) => sum + weekRequests(site),
        0,
      ),
      ward: protectedSites > 0 ? ward : undefined,
      wire:
        bus !== undefined
          ? [
              { x: -serverRadius, y: 0 },
              { x: trunk, y: 0 },
              { x: trunk, y: bus },
              { x, y: bus },
              { x, y: y - metrics.hub },
            ]
          : [
              { x: ux * serverRadius, y: uy * serverRadius },
              { x: x - ux * metrics.hub, y: y - uy * metrics.hub },
            ],
      titleText: shape.titleText,
      titleAt: { x: x + title.x, y: y + title.y },
      dim: filtering && matching === 0,
    };
  });

  // The picture spans the width, with the server in the middle; its height
  // is whatever the clusters need.
  let top = -serverRadius;
  let bottom = serverRadius;
  let left = -half;
  let right = half;
  for (const spot of spots) {
    top = Math.min(top, spot.box.y1);
    bottom = Math.max(bottom, spot.box.y2);
    left = Math.min(left, spot.box.x1);
    right = Math.max(right, spot.box.x2);
  }
  const dx = margin - left;
  const dy = margin - top;
  const shift = <T extends { x: number; y: number }>(p: T): T => ({
    ...p,
    x: round(p.x + dx),
    y: round(p.y + dy),
  });
  return {
    width: Math.ceil(right - left + 2 * margin),
    height: Math.ceil(bottom - top + 2 * margin),
    metrics,
    server: { x: round(dx), y: round(dy) },
    maxRequests,
    sites: sites.length,
    hubs: hubs.map((hub) => ({
      ...shift(hub),
      r: round(hub.r),
      dots: hub.dots.map((dot) => ({
        ...shift(dot),
        r: round(dot.r),
        label: dot.label ? shift(dot.label) : undefined,
      })),
      ward: hub.ward ? shift(hub.ward) : undefined,
      wire: hub.wire.map(shift),
      titleAt: shift(hub.titleAt),
    })),
  };
}

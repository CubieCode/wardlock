// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { Group } from './site-page-organise.js';
import {
  access,
  weekRequests,
  type SiteSummary,
} from './sites-page-sections.js';

/** The server node's radius, in map units (CSS pixels at full size). */
export const serverRadius = 44;
/** A group hub's radius. */
export const hubRadius = 22;
/** The smallest and largest site dot: sizes are capped at `maxDot`. */
export const minDot = 4;
export const maxDot = 11;
/** The most dots one hub draws; the rest are in the text list. */
export const dotCap = 60;

// Sites spiral out from their hub on a sunflower (Vogel) pattern: the k-th
// drawn site sits at a fixed angle and distance, so a new site adds a dot at
// the edge and never moves the others.
const golden = Math.PI * (3 - Math.sqrt(5));
const spiralStep = 14;
const firstRing = hubRadius + maxDot + 8;
// Room between clusters, and around the outside for group titles.
const clusterGap = 36;
const margin = 28;
const titleSize = 13;
const labelSize = 11;
// Rough advance of a monospace character at each size, for keeping titles
// and labels inside the picture.
const charWidth = (size: number) => size * 0.62;

export type Access = 'protected' | 'public' | 'down';

export interface MapDot {
  site: SiteSummary;
  x: number;
  y: number;
  r: number;
  access: Access;
  requests: number;
  /** Filters are on and the site does not match them. */
  dim: boolean;
}

export interface MapHub {
  /** `g:<id>`, or `g:none` for the ungrouped sites. */
  key: string;
  /** The group's ID, or `none`. */
  group: string;
  title: string;
  x: number;
  y: number;
  /** Every site in the group, in placement order. */
  sites: SiteSummary[];
  /** The drawn sites: all of them, or `dotCap` when there are more. */
  dots: MapDot[];
  /** How many of the group's sites have no dot. */
  hidden: number;
  /** How many match the filters (all of them when there are none). */
  matching: number;
  protectedSites: number;
  requests: number;
  /** The group has protected sites: a ward sits on its wire. */
  ward?: { x: number; y: number } | undefined;
  /** Where the server's wire meets the hub. */
  wire: { x1: number; y1: number; x2: number; y2: number };
  titleAt: { x: number; y: number; anchor: 'start' | 'middle' | 'end' };
  dim: boolean;
}

export interface MapLayout {
  width: number;
  height: number;
  server: { x: number; y: number };
  hubs: MapHub[];
  /** The 7-day request count the largest dot stands for. */
  maxRequests: number;
  sites: number;
}

/** A dot's radius: area grows with requests, capped at `maxDot`. */
export function dotRadius(requests: number, maxRequests: number) {
  if (maxRequests <= 0 || requests <= 0) return minDot;
  return (
    minDot +
    (maxDot - minDot) * Math.sqrt(Math.min(requests, maxRequests) / maxRequests)
  );
}

const byAge = (a: SiteSummary, b: SiteSummary) =>
  a.createdAt - b.createdAt || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);

/**
 * The sites a hub draws when it has more than `cap`: pinned first, then
 * those matching the filters, then the busiest, then by name; drawn in
 * placement order.
 */
function drawnSites(
  sites: SiteSummary[],
  matches: Set<string> | undefined,
  cap: number,
) {
  if (sites.length <= cap) return sites;
  const chosen = new Set(
    sites
      .map((site) => ({ site, requests: weekRequests(site) }))
      .toSorted(
        (a, b) =>
          Number(b.site.pinned) - Number(a.site.pinned) ||
          Number(matches?.has(b.site.id) ?? false) -
            Number(matches?.has(a.site.id) ?? false) ||
          b.requests - a.requests ||
          (a.site.name < b.site.name ? -1 : 1),
      )
      .slice(0, cap)
      .map(({ site }) => site),
  );
  return sites.filter((site) => chosen.has(site));
}

/** How far the k-th dot sits from its hub. */
const spiralRadius = (k: number) => firstRing + spiralStep * Math.sqrt(k);

/** A cluster's radius: its farthest dot, or just the hub. */
const clusterRadius = (dots: number) =>
  dots === 0 ? hubRadius + 6 : spiralRadius(dots - 1) + maxDot;

/**
 * The map: the server at the centre, one hub per group in `groups` order and
 * then No group (when it has sites), each with its sites around it. Pure and
 * deterministic: the same sites and groups always give the same picture.
 * `matches` holds the IDs of the sites the filters match; leave it out when
 * nothing is filtered.
 */
export function layoutMap(
  sites: SiteSummary[],
  groups: Group[],
  matches?: Set<string>,
  cap = dotCap,
): MapLayout {
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
    const drawn = drawnSites(cluster.sites, matches, cap);
    return { ...cluster, drawn, radius: clusterRadius(drawn.length) };
  });

  const maxRequests = Math.max(0, ...sites.map(weekRequests));

  // Hubs on an ellipse around the server, each given an arc in proportion
  // to its cluster's size, pushed outwards until no two clusters overlap.
  const total = clusters.reduce((sum, c) => sum + 2 * c.radius + clusterGap, 0);
  let before = 0;
  const angles = clusters.map((c) => {
    const share = 2 * c.radius + clusterGap;
    const angle = -Math.PI / 2 + (2 * Math.PI * (before + share / 2)) / total;
    before += share;
    return angle;
  });
  const stretch = clusters.length > 2 ? 1.45 : 1;
  const largest = Math.max(0, ...clusters.map((c) => c.radius));
  let distance = serverRadius + clusterGap + largest;
  const place = (d: number) =>
    angles.map((angle) => ({
      x: Math.cos(angle) * d * stretch,
      y: Math.sin(angle) * d,
    }));
  let centres = place(distance);
  const overlaps = () =>
    centres.some((a, i) =>
      centres.some(
        (b, j) =>
          j > i &&
          Math.hypot(a.x - b.x, a.y - b.y) <
            clusters[i]!.radius + clusters[j]!.radius + clusterGap,
      ),
    );
  for (let tries = 0; tries < 200 && overlaps(); tries++) {
    distance *= 1.05;
    centres = place(distance);
  }

  const filtering = matches !== undefined;
  const hubs = clusters.map((cluster, i): MapHub => {
    const { x, y } = centres[i]!;
    const away = Math.hypot(x, y) || 1;
    const ux = x / away;
    const uy = y / away;
    const dots = cluster.drawn.map((site, k): MapDot => {
      const theta = -Math.PI / 2 + k * golden;
      const rho = spiralRadius(k);
      const requests = weekRequests(site);
      return {
        site,
        x: x + Math.cos(theta) * rho,
        y: y + Math.sin(theta) * rho,
        r: dotRadius(requests, maxRequests),
        access: access(site),
        requests,
        dim: filtering && !matches.has(site.id),
      };
    });
    const matching = filtering
      ? cluster.sites.filter((site) => matches.has(site.id)).length
      : cluster.sites.length;
    const protectedSites = cluster.sites.filter(
      (site) => access(site) === 'protected',
    ).length;
    // The ward sits between the server and the near edge of the cluster.
    const wardAt = (serverRadius + away - cluster.radius) / 2;
    const titleOut = cluster.radius + 16;
    const tx = x + ux * titleOut;
    const ty = y + uy * titleOut + (uy > 0.3 ? titleSize : uy < -0.3 ? 0 : 4);
    return {
      key: `g:${cluster.group}`,
      group: cluster.group,
      title: cluster.title,
      x,
      y,
      sites: cluster.sites,
      dots,
      hidden: cluster.sites.length - dots.length,
      matching,
      protectedSites,
      requests: cluster.sites.reduce(
        (sum, site) => sum + weekRequests(site),
        0,
      ),
      ward: protectedSites > 0 ? { x: ux * wardAt, y: uy * wardAt } : undefined,
      wire: {
        x1: ux * serverRadius,
        y1: uy * serverRadius,
        x2: x - ux * hubRadius,
        y2: y - uy * hubRadius,
      },
      titleAt: {
        x: tx,
        y: ty,
        anchor: ux > 0.3 ? 'start' : ux < -0.3 ? 'end' : 'middle',
      },
      dim: filtering && matching === 0,
    };
  });

  // The picture's bounds: the server, every cluster, title and label.
  let left = -serverRadius;
  let right = serverRadius;
  let top = -serverRadius;
  let bottom = serverRadius;
  const grow = (x1: number, y1: number, x2: number, y2: number) => {
    left = Math.min(left, x1);
    right = Math.max(right, x2);
    top = Math.min(top, y1);
    bottom = Math.max(bottom, y2);
  };
  for (const [i, hub] of hubs.entries()) {
    const r = clusters[i]!.radius;
    grow(hub.x - r, hub.y - r, hub.x + r, hub.y + r);
    const w = hub.title.length * charWidth(titleSize);
    const { x, y, anchor } = hub.titleAt;
    const x1 = anchor === 'start' ? x : anchor === 'end' ? x - w : x - w / 2;
    grow(x1, y - titleSize, x1 + w, y + 4);
    for (const dot of hub.dots)
      if (dot.site.pinned) {
        const lw = dot.site.name.length * charWidth(labelSize);
        grow(
          dot.x - lw / 2,
          dot.y,
          dot.x + lw / 2,
          dot.y + dot.r + 4 + labelSize,
        );
      }
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
    server: { x: round(dx), y: round(dy) },
    maxRequests,
    sites: sites.length,
    hubs: hubs.map((hub) => ({
      ...shift(hub),
      dots: hub.dots.map((dot) => ({ ...shift(dot), r: round(dot.r) })),
      ward: hub.ward ? shift(hub.ward) : undefined,
      wire: {
        x1: round(hub.wire.x1 + dx),
        y1: round(hub.wire.y1 + dy),
        x2: round(hub.wire.x2 + dx),
        y2: round(hub.wire.y2 + dy),
      },
      titleAt: { ...shift(hub.titleAt), anchor: hub.titleAt.anchor },
    })),
  };
}

const round = (n: number) => Math.round(n * 10) / 10;

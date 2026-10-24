// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, it } from 'vitest';
import { countryPoints, land, mapHeight, mapWidth } from './world.gen.js';

/** The land path's rings, each a list of [x, y] points. */
const rings = land
  .split('Z')
  .filter(Boolean)
  .map((ring) =>
    ring
      .slice(1)
      .split('L')
      .map((point) => point.split(' ').map(Number) as [number, number]),
  );

describe('world map geometry', () => {
  it('has no land segment wrapping across the seam', () => {
    expect(rings.length).toBeGreaterThan(100);
    const wide = rings.flatMap((ring) =>
      ring.flatMap((point, i) => {
        const next = ring[(i + 1) % ring.length]!;
        return Math.abs(next[0] - point[0]) > mapWidth / 2
          ? [[point, next]]
          : [];
      }),
    );
    expect(wide).toEqual([]);
  });

  it('keeps every point inside the map', () => {
    const points = [...rings.flat(), ...Object.values(countryPoints)];
    for (const [x, y] of points) {
      expect(x).toBeGreaterThanOrEqual(0);
      expect(x).toBeLessThanOrEqual(mapWidth);
      expect(y).toBeGreaterThanOrEqual(0);
      expect(y).toBeLessThanOrEqual(mapHeight);
    }
  });
});

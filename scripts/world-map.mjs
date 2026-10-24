#!/usr/bin/env node
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Writes apps/web/src/analytics/world.gen.ts, the land outline and country
// points the analytics country map draws, from Natural Earth (public domain).
// Run it by hand when the projection or the source changes; it downloads two
// pinned files, checked against their SHA-256, so it is not part of
// `pnpm codegen`:
//   node scripts/world-map.mjs
// The file it writes records the SHA-256 of this script and of its own
// contents. `pnpm check:codegen` runs the offline check, which fails when
// this script changed since the file was written or the file was edited:
//   node scripts/world-map.mjs --check
//
// The projection is equirectangular, centred on 150°E so the Pacific sits in
// the middle, cropped to latitudes 75°N to 56°S (Antarctica left out). Land
// rings that cross the seam (30°W, through Greenland and Iceland) are clipped
// in two. Each country's point is Natural Earth's label point, keyed by its
// ISO 3166-1 alpha-2 code.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const release = 'v5.1.2';
const base = `https://raw.githubusercontent.com/nvkelso/natural-earth-vector/${release}/geojson`;
const landFile = 'ne_110m_land.geojson';
const countriesFile = 'ne_50m_admin_0_countries.geojson';
/** SHA-256 of each pinned file; a download that differs is refused. */
const checksums = {
  [landFile]:
    '9e0729ee253ca7d7a5c4ae9395fb1902264c5377c52e224d13dd85010e2835d9',
  [countriesFile]:
    '3e458fc036ad0a66411f2c1e6cac49c5d7bfb81cb1123bc513b22511a2b7fdeb',
};

const width = 720;
const centre = 150;
const north = 75;
const south = -56;
const height = Math.round(((north - south) / 360) * width);
const seam = centre - 180;

/** @param {number} lon */
const projectX = (lon) => (((lon - seam + 720) % 360) / 360) * width;
/** @param {number} lat */
const projectY = (lat) => ((north - lat) / (north - south)) * height;

/**
 * Sutherland–Hodgman clip of a ring against one side of the seam, in
 * longitude space where the ring does not wrap.
 * @param {number[][]} ring
 * @param {(lon: number) => boolean} inside
 */
function clip(ring, inside) {
  /** @type {number[][]} */
  const out = [];
  for (let i = 0; i < ring.length; i++) {
    const a = ring[i] ?? [0, 0];
    const b = ring[(i + 1) % ring.length] ?? [0, 0];
    const [ax = 0, ay = 0] = a;
    const [bx = 0, by = 0] = b;
    const ain = inside(ax);
    const bin = inside(bx);
    if (ain) out.push(a);
    if (ain !== bin) {
      const t = (seam - ax) / (bx - ax);
      // Just inside the side being kept, so it projects to that map edge.
      const edge = inside(seam) ? seam + 1e-9 : seam - 1e-9;
      out.push([edge, ay + t * (by - ay)]);
    }
  }
  return out;
}

/** @param {number[][]} ring */
function ringPath(ring) {
  /** @type {string[]} */
  const points = [];
  let last = '';
  for (const [lon = 0, lat = 0] of ring) {
    const y = Math.min(Math.max(lat, south), north);
    const point = `${projectX(lon).toFixed(1)} ${projectY(y).toFixed(1)}`;
    if (point !== last) points.push(point);
    last = point;
  }
  return points.length < 3 ? '' : `M${points.join('L')}Z`;
}

const script = fileURLToPath(import.meta.url);
const out = join(dirname(script), '../apps/web/src/analytics/world.gen.ts');
const sha256 = (/** @type {string | Buffer} */ data) =>
  createHash('sha256').update(data).digest('hex');
/** The header line holding the hashes, and the same line before they are known. */
const stampLine =
  /^\/\/ Stamp: generator ([0-9a-f]{64}), contents ([0-9a-f]{64})$/m;
const unstamped = '// Stamp: generator -, contents -';
const stamp = (
  /** @type {string} */ generator,
  /** @type {string} */ contents,
) => `// Stamp: generator ${generator}, contents ${contents}`;

/**
 * Fails unless world.gen.ts was written by this exact script and has not
 * been edited since. Needs no network.
 */
function check() {
  const text = readFileSync(out, 'utf8');
  const found = stampLine.exec(text);
  const problem = !found
    ? 'has no stamp'
    : found[1] !== sha256(readFileSync(script))
      ? 'was written by an older scripts/world-map.mjs'
      : found[2] !== sha256(text.replace(stampLine, unstamped))
        ? 'was edited by hand'
        : '';
  if (problem) {
    console.error(
      `apps/web/src/analytics/world.gen.ts ${problem}. Run node scripts/world-map.mjs and commit the result.`,
    );
    process.exit(1);
  }
  console.log('The world map data is up to date.');
}

/** @param {keyof typeof checksums} file */
async function download(file) {
  const response = await fetch(`${base}/${file}`);
  if (!response.ok) throw new Error(`${file}: HTTP ${response.status}`);
  const body = Buffer.from(await response.arrayBuffer());
  const sum = createHash('sha256').update(body).digest('hex');
  if (sum !== checksums[file])
    throw new Error(`${file}: SHA-256 ${sum}, expected ${checksums[file]}`);
  return /** @type {any} */ (JSON.parse(body.toString('utf8')));
}

async function main() {
  const land = await download(landFile);
  const countries = await download(countriesFile);

  /** @type {string[]} */
  const paths = [];
  for (const feature of land.features) {
    const { type, coordinates } = feature.geometry;
    const polygons = type === 'Polygon' ? [coordinates] : coordinates;
    for (const polygon of polygons) {
      const ring = /** @type {number[][]} */ (polygon[0]);
      if (ring.every(([, lat = 0]) => lat < south)) continue;
      const lons = ring.map(([lon = 0]) => lon);
      const crosses = Math.min(...lons) < seam && Math.max(...lons) > seam;
      const parts = crosses
        ? [clip(ring, (lon) => lon < seam), clip(ring, (lon) => lon >= seam)]
        : [ring];
      for (const part of parts) {
        const path = ringPath(part);
        if (path) paths.push(path);
      }
    }
  }

  /** @type {Record<string, [number, number]>} */
  const points = {};
  for (const { properties: p } of countries.features) {
    const code = [p.ISO_A2_EH, p.ISO_A2].find(
      (c) => typeof c === 'string' && /^[A-Z]{2}$/.test(c),
    );
    if (!code || points[code]) continue;
    const lat = Math.min(Math.max(p.LABEL_Y, south), north);
    points[code] = [
      Number(projectX(p.LABEL_X).toFixed(1)),
      Number(projectY(lat).toFixed(1)),
    ];
  }
  const sorted = Object.fromEntries(
    Object.entries(points).sort(([a], [b]) => a.localeCompare(b)),
  );

  writeFileSync(
    out,
    `// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
//
// Code generated by scripts/world-map.mjs; DO NOT EDIT.
// Source: Natural Earth ${release} (public domain, naturalearthdata.com),
// ${landFile} for land and ${countriesFile} for label points.
// Equirectangular, centred on ${centre}°E, latitudes ${north}°N to ${-south}°S.
${unstamped}

/** The map's size in SVG units. */
export const mapWidth = ${width};
export const mapHeight = ${height};

/** Land as one SVG path. */
export const land =
  ${JSON.stringify(paths.join(''))};

/** Each country's label point, [x, y] in map units, by ISO 3166-1 alpha-2 code. */
export const countryPoints: Record<string, readonly [number, number]> = ${JSON.stringify(sorted)};
`,
  );
  execFileSync('pnpm', ['exec', 'prettier', '--write', out], {
    stdio: 'inherit',
  });
  // Stamped after formatting, so the hash covers the committed text.
  const text = readFileSync(out, 'utf8');
  writeFileSync(
    out,
    text.replace(unstamped, stamp(sha256(readFileSync(script)), sha256(text))),
  );
}

if (process.argv[1] === script) {
  if (process.argv[2] === '--check') check();
  else await main();
}

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/*
 * Every colour role has a light and a dark value, the pairs components draw
 * meet WCAG 2.2 AA (4.5:1 text, 3:1 non-text) in both themes, the tag slots
 * and chart pair pass the dataviz palette checks, and colour literals live
 * only in tokens.css.
 */
const styles = fileURLToPath(new URL('../styles/', import.meta.url));
const css = readFileSync(join(styles, 'tokens.css'), 'utf8');

const roles = new Map<string, [string, string]>();
for (const [, name, light, dark] of css.matchAll(
  /(--color-[\w-]+):\s*light-dark\((#[0-9a-f]{6}),\s*(#[0-9a-f]{6})\);/g,
))
  roles.set(name!, [light!, dark!]);

const themes = ['light', 'dark'] as const;
const value = (role: string, theme: 0 | 1) => {
  const pair = roles.get(role);
  if (!pair) throw new Error(`${role} is not a colour role`);
  return pair[theme];
};

const rgb = (hex: string) =>
  [1, 3, 5].map((at) => {
    const v = parseInt(hex.slice(at, at + 2), 16) / 255;
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
const luminance = (hex: string) => {
  const [r, g, b] = rgb(hex);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a: string, b: string) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
};

// [foreground, background, minimum ratio]
const surfaces = ['--color-canvas', '--color-surface', '--color-surface-muted'];
const tones = ['success', 'warning', 'danger', 'neutral', 'ward', 'public'];
const tags = Array.from({ length: 9 }, (_, slot) => `--color-tag-${slot}`);
const charts = ['--color-chart-allowed', '--color-chart-refused'];
const pairs: [string, string, number][] = [
  ...surfaces.flatMap((bg): [string, string, number][] => [
    ['--color-text', bg, 4.5],
    ['--color-text-muted', bg, 4.5],
    ['--color-primary', bg, 4.5],
    ['--color-primary-hover', bg, 4.5],
    ['--color-border-strong', bg, 3],
    ['--color-focus', bg, 3],
    ...tags.map((tag): [string, string, number] => [tag, bg, 3]),
    ...charts.map((chart): [string, string, number] => [chart, bg, 3]),
  ]),
  ['--color-text', '--color-primary-soft', 4.5],
  ['--color-primary', '--color-primary-soft', 4.5],
  ['--color-text-on-primary', '--color-primary', 4.5],
  ['--color-text-on-primary', '--color-primary-hover', 4.5],
  ['--color-surface', '--color-danger', 4.5],
  ...tones.flatMap((tone): [string, string, number][] => [
    [`--color-${tone}`, `--color-${tone}-surface`, 4.5],
    [`--color-${tone}`, '--color-surface', 4.5],
    [`--color-${tone}`, '--color-canvas', 4.5],
  ]),
];

/*
 * The dataviz palette checks (Machado 2009 CVD simulation, OKLab distances
 * x100): lightness band, chroma floor, adjacent-pair separation for
 * protanopia and deuteranopia, and a normal-vision floor.
 */
const machado = {
  protan: [
    [0.152286, 1.052583, -0.204868],
    [0.114503, 0.786281, 0.099216],
    [-0.003882, -0.048116, 1.051998],
  ],
  deutan: [
    [0.367322, 0.860646, -0.227968],
    [0.280085, 0.672501, 0.047413],
    [-0.01182, 0.04294, 0.968881],
  ],
};
const oklab = ([r, g, b]: [number, number, number]) => {
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
};
const simulate = (hex: string, kind?: keyof typeof machado) => {
  const c = rgb(hex);
  if (!kind) return c;
  const clamp = (v: number) => Math.max(0, Math.min(1, v));
  return machado[kind].map((row) =>
    clamp(row[0]! * c[0] + row[1]! * c[1] + row[2]! * c[2]),
  ) as [number, number, number];
};
const deltaE = (a: string, b: string, kind?: keyof typeof machado) => {
  const [x, y] = [oklab(simulate(a, kind)), oklab(simulate(b, kind))];
  return 100 * Math.hypot(x[0]! - y[0]!, x[1]! - y[1]!, x[2]! - y[2]!);
};
const band = { light: [0.43, 0.77], dark: [0.48, 0.67] } as const;

const palettes: [string, string[]][] = [
  ['tag slots 1-8', tags.slice(1)],
  ['chart pair', charts],
];

describe('design tokens', () => {
  it('defines every colour role with light-dark()', () => {
    const declared = [...css.matchAll(/(--color-[\w-]+):/g)].map((m) => m[1]);
    expect(declared.length).toBeGreaterThan(0);
    expect(declared.filter((name) => !roles.has(name!))).toEqual([]);
  });

  for (const theme of [0, 1] as const)
    for (const [fg, bg, min] of pairs)
      it(`${fg} on ${bg} meets ${min}:1 in ${themes[theme]}`, () => {
        const ratio = contrast(value(fg, theme), value(bg, theme));
        expect(ratio).toBeGreaterThanOrEqual(min);
      });

  for (const theme of [0, 1] as const)
    for (const [name, slots] of palettes)
      describe(`${name} in ${themes[theme]}`, () => {
        const hexes = slots.map((slot) => value(slot, theme));
        const adjacent = hexes.slice(1).map((hex, i) => [hexes[i]!, hex]);

        it('sit in the lightness band with enough chroma', () => {
          const [lo, hi] = band[themes[theme]];
          for (const hex of hexes) {
            const [L, a, b] = oklab(rgb(hex));
            expect(L, hex).toBeGreaterThanOrEqual(lo);
            expect(L, hex).toBeLessThanOrEqual(hi);
            expect(Math.hypot(a!, b!), hex).toBeGreaterThanOrEqual(0.1);
          }
        });

        it('keep neighbours apart for colour-blind and full-colour readers', () => {
          for (const [a, b] of adjacent) {
            expect(deltaE(a!, b!, 'protan'), `${a} ${b}`).toBeGreaterThan(8);
            expect(deltaE(a!, b!, 'deutan'), `${a} ${b}`).toBeGreaterThan(8);
            expect(deltaE(a!, b!), `${a} ${b}`).toBeGreaterThan(15);
          }
        });
      });

  it('keeps colour literals in tokens.css', () => {
    const root = fileURLToPath(new URL('../../../', import.meta.url));
    const literal =
      /#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|oklch|oklab)\(|\b(?:white|black)\b(?!-)/;
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir)) {
        const path = join(dir, entry);
        if (entry === 'node_modules' || entry === 'dist') continue;
        if (statSync(path).isDirectory()) walk(path);
        else if (/\.(css|tsx?)$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
          if (path.endsWith(join('styles', 'tokens.css'))) continue;
          const text = readFileSync(path, 'utf8').replace(
            /\/\*[\s\S]*?\*\//g,
            '',
          );
          if (literal.test(text)) offenders.push(path.slice(root.length));
        }
      }
    };
    walk(join(root, 'packages/ui'));
    walk(join(root, 'apps/web/src'));
    expect(offenders).toEqual([]);
  });
});

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/*
 * Every colour role has a light and a dark value, the pairs components draw
 * meet WCAG 2.2 AA (4.5:1 text, 3:1 non-text) in both themes, and colour
 * literals live only in tokens.css.
 */
const styles = fileURLToPath(new URL('../styles/', import.meta.url));
const css = readFileSync(join(styles, 'tokens.css'), 'utf8');

const roles = new Map<string, [string, string]>();
for (const [, name, light, dark] of css.matchAll(
  /(--color-[\w-]+):\s*light-dark\((#[0-9a-f]{6}),\s*(#[0-9a-f]{6})\);/g,
))
  roles.set(name!, [light!, dark!]);

const channel = (hex: string, at: number) => {
  const v = parseInt(hex.slice(at, at + 2), 16) / 255;
  return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
};
const luminance = (hex: string) =>
  0.2126 * channel(hex, 1) +
  0.7152 * channel(hex, 3) +
  0.0722 * channel(hex, 5);
const contrast = (a: string, b: string) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
};

// [foreground, background, minimum ratio]
const pairs: [string, string, number][] = [
  ['--color-text', '--color-canvas', 4.5],
  ['--color-text', '--color-surface', 4.5],
  ['--color-text', '--color-surface-muted', 4.5],
  ['--color-text-muted', '--color-canvas', 4.5],
  ['--color-text-muted', '--color-surface', 4.5],
  ['--color-text-muted', '--color-surface-muted', 4.5],
  ['--color-text-on-primary', '--color-primary', 4.5],
  ['--color-text-on-primary', '--color-primary-hover', 4.5],
  ['--color-primary', '--color-surface', 4.5],
  ['--color-primary', '--color-canvas', 4.5],
  ['--color-success', '--color-success-surface', 4.5],
  ['--color-warning', '--color-warning-surface', 4.5],
  ['--color-danger', '--color-danger-surface', 4.5],
  ['--color-danger', '--color-surface', 4.5],
  ['--color-surface', '--color-danger', 4.5],
  ['--color-primary', '--color-surface-muted', 3],
  ['--color-border-strong', '--color-surface', 3],
  ['--color-focus', '--color-surface', 3],
  ['--color-focus', '--color-canvas', 3],
  ['--color-accent', '--color-surface', 3],
];

describe('design tokens', () => {
  it('defines every colour role with light-dark()', () => {
    const declared = [...css.matchAll(/(--color-[\w-]+):/g)].map((m) => m[1]);
    expect(declared.length).toBeGreaterThan(0);
    expect(declared.filter((name) => !roles.has(name!))).toEqual([]);
  });

  for (const theme of [0, 1] as const)
    for (const [fg, bg, min] of pairs)
      it(`${fg} on ${bg} meets ${min}:1 in ${theme ? 'dark' : 'light'}`, () => {
        const ratio = contrast(roles.get(fg)![theme], roles.get(bg)![theme]);
        expect(ratio).toBeGreaterThanOrEqual(min);
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

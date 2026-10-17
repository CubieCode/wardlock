// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { matchPath, safeReturnPath } from './router.js';

describe('matchPath', () => {
  it.each([
    ['/', '/', {}],
    ['/', '/missing', null],
    ['/sites/:name', '/sites/docs', { name: 'docs' }],
    ['/sites/:name', '/sites/docs/', { name: 'docs' }],
    ['/sites/:name', '/sites/', null],
    ['/sites/:name', '/sites/a%20b', { name: 'a b' }],
    ['/sites/:name', '/sites/%E0%A4%A', null],
    ['/sites/:name', '/sites/docs/deploys', null],
  ])('%s against %s', (pattern, path, want) => {
    expect(matchPath(pattern, path)).toEqual(want);
  });
});

describe('safeReturnPath', () => {
  it.each([
    [null, '/'],
    ['', '/'],
    ['/sites?x=1', '/sites?x=1'],
    ['https://evil.example/', '/'],
    ['//evil.example', '/'],
    ['/\\evil.example', '/'],
    ['/\t/evil.example', '/'],
    ['javascript:alert(1)', '/'],
  ])('%j becomes %j', (value, want) => {
    expect(safeReturnPath(value)).toBe(want);
  });
});

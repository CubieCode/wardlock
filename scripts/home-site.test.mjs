// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Checks home-site/, the page wardlock.nz serves, against the Go modules: one
// go-import tag per module in go.work, naming the module path, this
// repository and the module's directory, and a deploy with the SPA fallback
// so package paths below each module answer the same page.
// scripts/test-module-tags.sh runs `go install` through the page itself.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const root = resolve(import.meta.dirname, '..');
const read = (/** @type {string} */ path) =>
  readFileSync(resolve(root, path), 'utf8');
const repository = 'https://github.com/CubieCode/wardlock';

/** The module directories go.work uses, such as apps/cli. */
function workspaceModules() {
  const use = /^use \(([^)]*)\)/m.exec(read('go.work'))?.[1] ?? '';
  return use
    .split('\n')
    .map((line) => line.trim().replace(/^\.\//, ''))
    .filter((dir) => dir !== '' && !dir.startsWith('//'));
}

/** The content of every go-import meta tag in the page, in order. */
function goImports(/** @type {string} */ html) {
  return [
    ...html.matchAll(/<meta\s+name="go-import"\s+content="([^"]*)"/g),
  ].map((m) => m[1]);
}

describe('home-site', () => {
  const html = read('home-site/public/index.html');

  it('has one go-import tag for each module in go.work', () => {
    const dirs = workspaceModules();
    expect(dirs).toEqual(['apps/cli', 'apps/server', 'packages/apiclient-go']);
    const want = dirs.map((dir) => {
      const module = /^module\s+(\S+)/m.exec(read(`${dir}/go.mod`))?.[1];
      return `${module} git ${repository} ${dir}`;
    });
    expect(goImports(html).sort()).toEqual(want.sort());
  });

  it('declares the tags in the head, before the body', () => {
    const head = html.slice(0, html.indexOf('<body'));
    expect(goImports(head)).toHaveLength(goImports(html).length);
  });

  it('deploys as the home site with the SPA fallback', () => {
    expect(JSON.parse(read('home-site/wardlock.json'))).toEqual({
      site: 'home',
      dir: 'public',
      spa: true,
    });
  });
});

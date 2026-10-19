// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import { posix } from 'node:path';

/** @param {string} html */
const idsIn = (html) =>
  new Set([...html.matchAll(/\sid="([^"]*)"/g)].map((m) => m[1]));

/**
 * Returns a problem for each broken internal link in a built site.
 * @param {Map<string, string>} files Built files by path relative to the site root.
 */
export function checkLinks(files) {
  const problems = [];
  for (const [path, html] of files) {
    if (!path.endsWith('.html')) continue;
    for (const [, attr, raw = ''] of html.matchAll(/\s(href|src)="([^"]*)"/g)) {
      const url = raw.replaceAll('&amp;', '&');
      if (/^[a-z][a-z0-9+.-]*:|^\/\//i.test(url)) {
        if (!/^(https?|mailto):/i.test(url))
          problems.push(`${path}: ${attr}="${raw}" uses an unexpected scheme`);
        continue;
      }
      const hashAt = url.indexOf('#');
      const target = hashAt === -1 ? url : url.slice(0, hashAt);
      const fragment =
        hashAt === -1 ? '' : decodeURIComponent(url.slice(hashAt + 1));
      const resolved = target
        ? posix.normalize(posix.join(posix.dirname(path), target))
        : path;
      const content = files.get(resolved);
      if (content === undefined) {
        problems.push(`${path}: ${attr}="${raw}" points to a missing file`);
        continue;
      }
      if (fragment && !idsIn(content).has(fragment))
        problems.push(`${path}: ${attr}="${raw}" points to a missing anchor`);
    }
  }
  return problems;
}

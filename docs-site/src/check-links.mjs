// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Checks every link in the built site without network access: each relative
// href or src must name a file in dist, and each fragment an id in its target
// page. Links to other origins are not fetched; the build already checked
// that repository links point to files that exist.
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { checkLinks } from './links.mjs';

const dist = fileURLToPath(new URL('../dist/', import.meta.url));
/** @type {Map<string, string>} */
const files = new Map();
for (const name of readdirSync(dist, { recursive: true, encoding: 'utf8' })) {
  if (name.endsWith('.html') || name.endsWith('.css'))
    files.set(name.replaceAll('\\', '/'), readFileSync(dist + name, 'utf8'));
}
const problems = checkLinks(files);
if (problems.length) {
  console.error(problems.join('\n'));
  console.error(`docs-site: ${problems.length} broken link(s)`);
  process.exit(1);
}
console.log(`docs-site: links in ${files.size} files are valid`);

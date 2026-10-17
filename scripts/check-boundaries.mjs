// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Licence and boundary checks that ESLint and golangci-lint cannot express:
// - every source file carries the SPDX identifier of the area it lives in;
// - package.json licence fields match their area;
// - MIT Go modules do not require the AGPL server module.
// Import-level boundaries live in eslint.config.js and .golangci.yml.
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

/** Directories under AGPL-3.0-only; everything else is MIT. */
export const agplAreas = ['apps/server/', 'apps/web/', 'packages/ui/'];

const sourceFile =
  /\.(go|ts|tsx|js|mjs|css|sql|sh|html|ya?ml)$|(^|\/)Dockerfile$/;
// Content and data that are not source code.
const exempt = [
  /^\.agents\//,
  /^\.claude\//,
  /^fixtures\//,
  /^\.github\//,
  /(^|\/)pnpm-lock\.yaml$/,
  /(^|\/)pnpm-workspace\.yaml$/,
  /^orca\.yaml$/,
];

/** @param {string} path */
export const licenceFor = (path) =>
  agplAreas.some((area) => path.startsWith(area)) ? 'AGPL-3.0-only' : 'MIT';

/**
 * Returns problems for one file's path and contents.
 * @param {string} path
 * @param {string} contents
 */
export function checkHeader(path, contents) {
  if (!sourceFile.test(path) || exempt.some((re) => re.test(path))) return [];
  const head = contents.split('\n').slice(0, 6).join('\n');
  const found = /SPDX-License-Identifier:\s*([\w.-]+)/.exec(head)?.[1];
  // SQL query files keep the header last (sqlc copies leading comments).
  const tail = /SPDX-License-Identifier:\s*([\w.-]+)/.exec(
    contents.split('\n').slice(-4).join('\n'),
  )?.[1];
  const want = licenceFor(path);
  const got = found ?? (path.endsWith('.sql') ? tail : undefined);
  if (!got) return [`${path}: missing "SPDX-License-Identifier: ${want}"`];
  if (got !== want) return [`${path}: licence ${got}, expected ${want}`];
  return [];
}

function main() {
  const files = execFileSync(
    'git',
    ['ls-files', '--cached', '--others', '--exclude-standard', '-z'],
    { encoding: 'utf8' },
  )
    .split('\0')
    .filter(Boolean);
  const problems = [];
  for (const path of files) {
    let contents;
    try {
      contents = readFileSync(path, 'utf8');
    } catch {
      continue; // Deleted in the working tree.
    }
    problems.push(...checkHeader(path, contents));
    if (path.endsWith('package.json') && path.includes('/')) {
      const { license } = JSON.parse(contents);
      if (license !== licenceFor(path))
        problems.push(
          `${path}: "license" is ${license}, expected ${licenceFor(path)}`,
        );
    }
    if (path.endsWith('go.mod') && licenceFor(path) === 'MIT') {
      if (/^\s*(require\s+)?wardlock\.nz\/server\b/m.test(contents))
        problems.push(
          `${path}: an MIT module must not require wardlock.nz/server`,
        );
    }
  }
  if (problems.length) {
    console.error(problems.join('\n'));
    process.exit(1);
  }
  console.log(`Licence headers and boundaries OK (${files.length} files).`);
}

if (import.meta.url === `file://${process.argv[1]}`) main();

// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Regenerates ROADMAP.md's status board and folds finished items.
//
//   node scripts/roadmap-board.mjs [--check]
//
// The board (between the roadmap-board markers) is built from each item's
// `- Status:` and `- PR:` fields, which stay the source of truth. In each area,
// items whose Status is `done` move into one collapsed block after the area's
// introduction; every other item stays expanded below it, in item order.
// Run it after changing a Status line. --check rewrites nothing and exits 1
// when the file is out of date (`pnpm check` runs it).
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export const START = '<!-- roadmap-board:start -->';
export const END = '<!-- roadmap-board:end -->';
const AREA = /^## ([A-Z]\d+): (.+)$/;
const ITEM = /^### ([A-Z]\d+\.(\d+)): (.+)$/;
const FOLD_SUMMARY = /^<summary>Done: \d+ of \d+ items<\/summary>$/;

/**
 * GitHub's heading anchor: lowercase, punctuation dropped, spaces to hyphens.
 * Matches `slugify` in docs-site/src/render.mjs.
 * @param {string} heading
 */
export function anchor(heading) {
  return heading
    .toLowerCase()
    .trim()
    .replace(/[^\p{L}\p{M}\p{N}\p{Pc} -]/gu, '')
    .replaceAll(' ', '-');
}

/**
 * The PR numbers in a PR field as Markdown links, or the field as written.
 * @param {string} pr
 */
export function prText(pr) {
  const links = [
    ...pr.matchAll(/https:\/\/github\.com\/[^\s/]+\/[^\s/]+\/pull\/(\d+)/g),
  ];
  return links.length
    ? links.map(([url, n]) => `[#${n}](${url})`).join(', ')
    : pr;
}

/** @param {string[]} lines */
function trimBlank(lines) {
  const start = lines.findIndex((line) => line.trim());
  if (start === -1) return [];
  const end = lines.findLastIndex((line) => line.trim());
  return lines.slice(start, end + 1);
}

/**
 * @typedef {{ id: string, number: number, title: string, heading: string, status: string, pr: string, lines: string[] }} Item
 * @typedef {{ id: string, heading: string, intro: string[], items: Item[] }} Area
 */

/**
 * Splits an area's body into its introduction and items, dropping the fold
 * this script adds.
 * @param {string} heading
 * @param {string[]} body
 * @returns {Area}
 */
function parseArea(heading, body) {
  const match = AREA.exec(heading);
  if (!match) throw new Error(`not an area heading: ${heading}`);
  /** @type {string[]} */
  const unfolded = [];
  let folded = false;
  for (const [n, line] of body.entries()) {
    if (line === '<details>' && FOLD_SUMMARY.test(body[n + 1] ?? '')) {
      folded = true;
    } else if (folded && FOLD_SUMMARY.test(line)) {
      continue;
    } else if (folded && line === '</details>') {
      folded = false;
    } else {
      unfolded.push(line);
    }
  }
  /** @type {string[]} */
  const intro = [];
  /** @type {Item[]} */
  const items = [];
  /** @type {Item | undefined} */
  let current;
  for (const line of unfolded) {
    const heading = ITEM.exec(line);
    if (heading) {
      current = {
        id: heading[1] ?? '',
        number: Number(heading[2]),
        title: heading[3] ?? '',
        heading: line.slice(4),
        status: '',
        pr: '',
        lines: [line],
      };
      items.push(current);
    } else if (current) {
      current.lines.push(line);
      const status = /^- Status: `([^`]*)`/.exec(line);
      if (status) current.status = status[1] ?? '';
      else if (line.startsWith('- PR:')) current.pr = line.slice(5).trim();
    } else {
      intro.push(line);
    }
  }
  for (const item of items) item.lines = trimBlank(item.lines);
  items.sort((a, b) => a.number - b.number);
  return { id: match[1] ?? '', heading, intro: trimBlank(intro), items };
}

/**
 * @param {Area[]} areas
 * @returns {string[]}
 */
export function board(areas) {
  const out = [
    START,
    '<!-- prettier-ignore-start -->',
    '',
    '| Area | Done | Open items |',
    '| --- | --- | --- |',
  ];
  for (const area of areas) {
    const done = area.items.filter((i) => i.status === 'done').length;
    const open = area.items
      .filter((i) => i.status !== 'done')
      .map((i) => `[${i.id}](#${anchor(i.heading)}) ${i.status}`)
      .join(', ');
    const title = `[${area.heading.slice(3)}](#${anchor(area.heading.slice(3))})`;
    out.push(`| ${title} | ${done}/${area.items.length} | ${open} |`);
  }
  out.push(
    '',
    '<details>',
    '<summary>All items</summary>',
    '',
    '| Item | Status | PR |',
    '| --- | --- | --- |',
  );
  for (const area of areas)
    for (const i of area.items)
      out.push(
        `| [${i.id}: ${i.title}](#${anchor(i.heading)}) | ${i.status} | ${prText(i.pr)} |`,
      );
  out.push('', '</details>', '', '<!-- prettier-ignore-end -->', END);
  return out;
}

/**
 * @param {Area} area
 * @returns {string[]}
 */
function renderArea(area) {
  const done = area.items.filter((i) => i.status === 'done');
  const open = area.items.filter((i) => i.status !== 'done');
  const out = [area.heading, ''];
  if (area.intro.length) out.push(...area.intro, '');
  if (done.length) {
    out.push(
      '<details>',
      `<summary>Done: ${done.length} of ${area.items.length} items</summary>`,
      '',
    );
    for (const item of done) out.push(...item.lines, '');
    out.push('</details>', '');
  }
  for (const item of open) out.push(...item.lines, '');
  return out;
}

/**
 * Returns ROADMAP.md with the board regenerated and finished items folded.
 * @param {string} text
 */
export function render(text) {
  const lines = text.split('\n');
  const start = lines.indexOf(START);
  const end = lines.indexOf(END);
  if (start === -1 || end < start)
    throw new Error(`ROADMAP.md: add the ${START} and ${END} markers`);
  /** @type {string[]} */
  const head = [];
  /** @type {{ heading: string, body: string[] }[]} */
  const sections = [];
  /** @type {string[]} */
  const tail = [];
  /** @type {{ heading: string, body: string[] } | undefined} */
  let section;
  for (const [n, line] of lines.entries()) {
    if (n >= start && n <= end) {
      if (n === start) head.push(START);
      continue;
    }
    if (AREA.test(line)) {
      section = { heading: line, body: [] };
      sections.push(section);
    } else if ((section && line.startsWith('## ')) || tail.length) {
      tail.push(line);
    } else if (section) {
      section.body.push(line);
    } else {
      head.push(line);
    }
  }
  const areas = sections.map((s) => parseArea(s.heading, s.body));
  const at = head.indexOf(START);
  head.splice(at, 1, ...board(areas));
  return [...head, ...areas.flatMap(renderArea), ...tail].join('\n');
}

function main() {
  const path = fileURLToPath(new URL('../ROADMAP.md', import.meta.url));
  const text = readFileSync(path, 'utf8');
  const next = render(text);
  if (process.argv.includes('--check')) {
    if (next !== text) {
      console.error(
        'ROADMAP.md: status board or folded items out of date; run node scripts/roadmap-board.mjs',
      );
      process.exit(1);
    }
    console.log('ROADMAP.md status board is up to date.');
    return;
  }
  if (next !== text) {
    writeFileSync(path, next);
    console.log('ROADMAP.md updated');
  }
}

if (import.meta.url === `file://${process.argv[1]}`) main();

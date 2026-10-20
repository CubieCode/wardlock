// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest';
import { END, START, anchor, prText, render } from './roadmap-board.mjs';

const item = (
  /** @type {string} */ id,
  /** @type {string} */ title,
  /** @type {string} */ status,
  pr = '',
) =>
  `### ${id}: ${title}\n\n- Status: \`${status}\`\n- Owner:\n- PR: ${pr}\n- Scope: Something.\n`;

const roadmap = (/** @type {string} */ body) =>
  `# Roadmap\n\nIntro.\n\n${START}\n${END}\n\n${body}\n## Waves\n\n1. **Wave 1:** X.\n`;

describe('anchor', () => {
  it.each([
    ['C1.12: Fewer object-store calls', 'c112-fewer-object-store-calls'],
    ['O4: Operations and release', 'o4-operations-and-release'],
    [
      'C1.1: Owner bootstrap, email-code sign-in and dashboard sessions',
      'c11-owner-bootstrap-email-code-sign-in-and-dashboard-sessions',
    ],
  ])('%s', (heading, want) => {
    expect(anchor(heading)).toBe(want);
  });
});

describe('prText', () => {
  it('links PR numbers and keeps other text', () => {
    expect(prText('https://github.com/CubieCode/wardlock/pull/26')).toBe(
      '[#26](https://github.com/CubieCode/wardlock/pull/26)',
    );
    expect(prText('')).toBe('');
    expect(prText('initial bootstrap')).toBe('initial bootstrap');
  });
});

describe('render', () => {
  const source = roadmap(
    `## X1: First area\n\nArea intro.\n\n${item('X1.1', 'Open thing', 'todo')}\n${item('X1.2', 'Built thing', 'done', 'https://github.com/o/r/pull/7')}\n${item('X1.10', 'Later thing', 'review')}\n`,
  );

  it('builds the board from Status and PR fields', () => {
    const out = render(source);
    expect(out).toContain(
      '| [X1: First area](#x1-first-area) | 1/3 | [X1.1](#x11-open-thing) todo, [X1.10](#x110-later-thing) review |',
    );
    expect(out).toContain(
      '| [X1.2: Built thing](#x12-built-thing) | done | [#7](https://github.com/o/r/pull/7) |',
    );
  });

  it('folds finished items after the intro and keeps open ones expanded', () => {
    const out = render(source);
    const area = out.slice(out.indexOf('## X1'), out.indexOf('## Waves'));
    expect(area).toBe(
      `## X1: First area\n\nArea intro.\n\n<details>\n<summary>Done: 1 of 3 items</summary>\n\n${item('X1.2', 'Built thing', 'done', 'https://github.com/o/r/pull/7')}\n</details>\n\n${item('X1.1', 'Open thing', 'todo')}\n${item('X1.10', 'Later thing', 'review')}\n`,
    );
    expect(out.endsWith('## Waves\n\n1. **Wave 1:** X.\n')).toBe(true);
  });

  it('is stable, and unfolds an item whose Status changes back', () => {
    const once = render(source);
    expect(render(once)).toBe(once);
    const reopened = render(
      once.replace(
        '- Status: `done`\n- Owner:\n- PR: https://github.com/o/r/pull/7',
        '- Status: `in-progress`\n- Owner:\n- PR: https://github.com/o/r/pull/7',
      ),
    );
    expect(reopened).not.toContain('<summary>Done:');
    expect(reopened).toContain('0/3');
  });

  it('needs the markers', () => {
    expect(() => render('# Roadmap\n')).toThrow(/markers/);
  });
});

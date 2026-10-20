// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  parseInline,
  parseMarkdown,
  type Block,
  type Inline,
} from './markdown.js';

describe('parseMarkdown', () => {
  it('reads every construct the policy uses', () => {
    const blocks = parseMarkdown(
      [
        '# Title',
        '',
        '> **Draft.** Read it.',
        '',
        'One line',
        'continues here.',
        '',
        '- first `item`;',
        '- second [link](https://example.test/a).',
        '',
        '<details>',
        '<summary>More</summary>',
        '',
        'Inside.',
        '',
        '</details>',
        '',
        '## Next',
      ].join('\n'),
    );
    expect(blocks).toEqual<Block[]>([
      { kind: 'h1', content: [{ kind: 'text', text: 'Title' }] },
      {
        kind: 'note',
        content: [
          { kind: 'strong', text: 'Draft.' },
          { kind: 'text', text: ' Read it.' },
        ],
      },
      {
        kind: 'p',
        content: [{ kind: 'text', text: 'One line continues here.' }],
      },
      {
        kind: 'list',
        items: [
          [
            { kind: 'text', text: 'first ' },
            { kind: 'code', text: 'item' },
            { kind: 'text', text: ';' },
          ],
          [
            { kind: 'text', text: 'second ' },
            { kind: 'link', text: 'link', href: 'https://example.test/a' },
            { kind: 'text', text: '.' },
          ],
        ],
      },
      {
        kind: 'details',
        summary: 'More',
        blocks: [{ kind: 'p', content: [{ kind: 'text', text: 'Inside.' }] }],
      },
      { kind: 'h2', content: [{ kind: 'text', text: 'Next' }] },
    ]);
  });

  it.each([
    ['a table', '| a | b |'],
    ['a numbered list', '1. one'],
    ['a deeper heading', '### Three'],
    ['other HTML', '<div>x</div>'],
    ['an unclosed block', '<details>\n<summary>x</summary>'],
    ['a stray close', '</details>'],
    ['a nested block', '<details>\n<summary>x</summary>\n<details>'],
    ['a block without a summary', '<details>\n\nText.\n\n</details>'],
  ])('refuses %s rather than showing it as raw text', (_, source) => {
    expect(() => parseMarkdown(source)).toThrow();
  });
});

describe('docs/ACCEPTABLE-USE.md', () => {
  const source = readFileSync(
    new URL('../../../../docs/ACCEPTABLE-USE.md', import.meta.url),
    'utf8',
  );
  const blocks = parseMarkdown(source);
  const inlines = (list: Block[]): Inline[] =>
    list.flatMap((block) =>
      block.kind === 'list'
        ? block.items.flat()
        : block.kind === 'details'
          ? [...parseInline(block.summary), ...inlines(block.blocks)]
          : block.content,
    );

  it('parses, with one title, the draft note and the sections', () => {
    expect(blocks.filter((b) => b.kind === 'h1')).toHaveLength(1);
    expect(blocks[1]?.kind).toBe('note');
    expect(
      blocks.flatMap((b) =>
        b.kind === 'h2' ? b.content.map((c) => c.text) : [],
      ),
    ).toEqual(['Screening, removal and suspension', 'Reporting abuse']);
  });

  it('links only to absolute https URLs, which work from the dashboard', () => {
    const links = inlines(blocks).filter((i) => i.kind === 'link');
    expect(links.length).toBeGreaterThan(0);
    for (const link of links)
      expect(link.kind === 'link' && link.href).toMatch(/^https:\/\//);
  });
});

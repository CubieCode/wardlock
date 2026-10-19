// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { checkLinks } from './links.mjs';
import { renderMarkdown, rewriteHref, slugify } from './render.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));

describe('slugify', () => {
  it.each([
    ['Custom domains (C1.9, D3.5)', 'custom-domains-c19-d35'],
    [
      '`wardlock.json` and `.wardlockignore` (C1.5)',
      'wardlockjson-and-wardlockignore-c15',
    ],
    [
      'Cookie tossing and cross-site session abuse',
      'cookie-tossing-and-cross-site-session-abuse',
    ],
    ['Behind Caddy or nginx', 'behind-caddy-or-nginx'],
  ])('%s', (text, want) => {
    expect(slugify(text.replaceAll('`', ''))).toBe(want);
  });
});

describe('rewriteHref', () => {
  it('maps published pages to their HTML files, keeping the fragment', () => {
    expect(rewriteHref('SPEC.md#serving-c13', 'docs/DEPLOYMENT.md', root)).toBe(
      'spec.html#serving-c13',
    );
  });
  it('sends other repository files to GitHub', () => {
    expect(
      rewriteHref('../deploy/Dockerfile', 'docs/DEPLOYMENT.md', root),
    ).toBe('https://github.com/CubieCode/wardlock/blob/main/deploy/Dockerfile');
    expect(rewriteHref('ARCHITECTURE.md', 'docs/SPEC.md', root)).toBe(
      'https://github.com/CubieCode/wardlock/blob/main/docs/ARCHITECTURE.md',
    );
  });
  it('keeps absolute URLs and fragments', () => {
    expect(rewriteHref('https://example.com/a', 'docs/SPEC.md', root)).toBe(
      'https://example.com/a',
    );
    expect(rewriteHref('#tls', 'docs/SPEC.md', root)).toBe('#tls');
  });
  it('refuses missing files and paths outside the repository', () => {
    expect(() => rewriteHref('NOPE.md', 'docs/SPEC.md', root)).toThrow(
      /missing file/,
    );
    expect(() => rewriteHref('../../x', 'docs/SPEC.md', root)).toThrow(
      /leaves/,
    );
  });
});

describe('renderMarkdown', () => {
  it('gives headings GitHub IDs, numbering repeats', () => {
    const { html, title, headings } = renderMarkdown(
      '# Title\n\n## Usage\n\n## Usage\n\n[x](SPEC.md)\n\n| a |\n| - |\n| b |\n',
      'docs/TEST.md',
      root,
    );
    expect(title).toBe('Title');
    expect(headings.map((h) => h.id)).toEqual(['title', 'usage', 'usage-1']);
    expect(html).toContain('<a href="spec.html">x</a>');
    expect(html).toContain('<div class="table-wrap"><table>');
  });
});

describe('checkLinks', () => {
  const site = (/** @type {Record<string, string>} */ files) =>
    checkLinks(new Map(Object.entries(files)));

  it('accepts valid files, anchors and external links', () => {
    expect(
      site({
        'index.html':
          '<a href="a.html#x">a</a><a href="#top">t</a><p id="top"></p><a href="https://example.com/">e</a><link href="style.css">',
        'a.html': '<h2 id="x">X</h2>',
        'style.css': '',
      }),
    ).toEqual([]);
  });
  it('reports missing files, missing anchors and odd schemes', () => {
    expect(
      site({
        'index.html':
          '<a href="gone.html">g</a><a href="a.html#nope">n</a><a href="javascript:alert(1)">j</a>',
        'a.html': '<h2 id="x">X</h2>',
      }),
    ).toEqual([
      'index.html: href="gone.html" points to a missing file',
      'index.html: href="a.html#nope" points to a missing anchor',
      'index.html: href="javascript:alert(1)" uses an unexpected scheme',
    ]);
  });
});

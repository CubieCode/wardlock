// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Renders the user-facing pages of docs/ to static HTML. Links between
// published pages become links between their HTML files; links to anything
// else in the repository become GitHub links, after checking the target
// exists, so a moved file fails the build instead of shipping a dead link.
import { existsSync, statSync } from 'node:fs';
import { join, normalize, posix } from 'node:path';
import { Marked } from 'marked';

export const repoUrl = 'https://github.com/CubieCode/wardlock';

/**
 * The published pages, in navigation order. Internal and contributor docs
 * (ARCHITECTURE, MODULES, DESIGN-SYSTEM) stay in the repository.
 * @type {{ source: string, output: string, nav: string, summary: string }[]}
 */
export const pages = [
  {
    source: 'docs/SPEC.md',
    nav: 'Specification',
    output: 'spec.html',
    summary:
      'What Wardlock does: sites, deploys, policies, share links, sign-in methods, AI-readable renditions and the CLI.',
  },
  {
    source: 'docs/DEPLOYMENT.md',
    nav: 'Self-hosting',
    output: 'deployment.html',
    summary:
      'Running your own instance: domains and DNS, TLS, systemd, Docker, reverse proxies, SMTP, OAuth, backups and upgrades.',
  },
  {
    source: 'docs/SECURITY-MODEL.md',
    nav: 'Security model',
    output: 'security-model.html',
    summary: 'What Wardlock defends against, how, and which risks it accepts.',
  },
];

/** @param {string} text */
export function escapeHtml(text) {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

/**
 * Heading IDs as GitHub assigns them, so `SPEC.md#serving-c13` links written
 * for the repository also work on the site.
 * @param {string} text Heading text without markup.
 */
export function slugify(text) {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^\p{L}\p{M}\p{N}\p{Pc} -]/gu, '')
    .replaceAll(' ', '-');
}

/**
 * The text of inline tokens without their markup.
 * @param {import('marked').Token[]} tokens
 * @returns {string}
 */
function plainText(tokens) {
  return tokens
    .map((token) =>
      'tokens' in token && token.tokens
        ? plainText(token.tokens)
        : 'text' in token
          ? decodeEntities(String(token.text))
          : '',
    )
    .join('');
}

/** @param {string} text */
function decodeEntities(text) {
  return text
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&quot;', '"')
    .replaceAll('&#39;', "'")
    .replaceAll('&amp;', '&');
}

/**
 * Maps a link in a source document to its href on the site.
 * @param {string} href The link as written.
 * @param {string} source The document's path relative to the repository root.
 * @param {string} root The repository root on disk.
 */
export function rewriteHref(href, source, root) {
  if (/^[a-z][a-z0-9+.-]*:|^\/\//i.test(href) || href.startsWith('#'))
    return href;
  const hashAt = href.indexOf('#');
  const path = hashAt === -1 ? href : href.slice(0, hashAt);
  const fragment = hashAt === -1 ? '' : href.slice(hashAt);
  const target = posix.normalize(posix.join(posix.dirname(source), path));
  if (target.startsWith('../') || posix.isAbsolute(path))
    throw new Error(`${source}: link ${href} leaves the repository`);
  const page = pages.find((p) => p.source === target);
  if (page) return page.output + fragment;
  const onDisk = join(root, normalize(target));
  if (!existsSync(onDisk))
    throw new Error(`${source}: link ${href} points to a missing file`);
  const kind = statSync(onDisk).isDirectory() ? 'tree' : 'blob';
  return `${repoUrl}/${kind}/main/${target}${fragment}`;
}

/**
 * Renders one Markdown document.
 * @param {string} markdown
 * @param {string} source The document's path relative to the repository root.
 * @param {string} root The repository root on disk.
 */
export function renderMarkdown(markdown, source, root) {
  /** @type {Map<string, number>} */
  const seen = new Map();
  /** @type {{ depth: number, id: string, text: string }[]} */
  const headings = [];
  const marked = new Marked({
    gfm: true,
    walkTokens(token) {
      if (token.type === 'link' || token.type === 'image')
        token.href = rewriteHref(token.href, source, root);
    },
    renderer: {
      heading({ tokens, depth }) {
        const text = plainText(tokens);
        const base = slugify(text);
        const count = seen.get(base) ?? 0;
        seen.set(base, count + 1);
        const id = count ? `${base}-${count}` : base;
        headings.push({ depth, id, text });
        return `<h${depth} id="${id}">${this.parser.parseInline(tokens)}</h${depth}>\n`;
      },
    },
  });
  const html = /** @type {string} */ (marked.parse(markdown))
    .replaceAll('<table>', '<div class="table-wrap"><table>')
    .replaceAll('</table>', '</table></div>');
  const title = headings.find((h) => h.depth === 1)?.text ?? source;
  return { html, title, headings };
}

/**
 * Wraps rendered content in the site's page template.
 * @param {{ title: string, body: string, current?: string, toc?: { id: string, text: string }[], sourceUrl?: string }} page
 */
export function layout({ title, body, current, toc = [], sourceUrl }) {
  const nav = pages
    .map((p) => {
      const here = p.output === current ? ' aria-current="page"' : '';
      return `<li><a href="${p.output}"${here}>${escapeHtml(p.nav)}</a></li>`;
    })
    .join('');
  const contents = toc.length
    ? `<details class="toc"><summary>On this page</summary><nav aria-label="On this page"><ul>${toc
        .map((h) => `<li><a href="#${h.id}">${escapeHtml(h.text)}</a></li>`)
        .join('')}</ul></nav></details>`
    : '';
  const source = sourceUrl
    ? ` <a href="${sourceUrl}">View the source of this page</a>.`
    : '';
  return `<!doctype html>
<html lang="en-NZ">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(title)} · Wardlock docs</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<header class="masthead"><a class="brand" href="index.html">Wardlock <span>docs</span></a></header>
<div class="shell">
<aside class="sidebar"><nav aria-label="Pages"><ul>${nav}</ul></nav>${contents}</aside>
<main id="main">
${body}
</main>
</div>
<footer>Wardlock is pre-alpha; these pages describe intended v1 behaviour and mark what is not built yet.${source}</footer>
</body>
</html>
`;
}

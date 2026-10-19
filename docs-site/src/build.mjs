// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Builds the documentation site into docs-site/dist: one HTML page per
// published document, an index, a 404 page and the stylesheet. The output is
// plain static files for `wardlock deploy` (see docs-site/README.md).
import {
  copyFileSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { fileURLToPath } from 'node:url';
import {
  escapeHtml,
  layout,
  pages,
  renderMarkdown,
  repoUrl,
} from './render.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
const out = fileURLToPath(new URL('../dist/', import.meta.url));

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

for (const page of pages) {
  const markdown = readFileSync(root + page.source, 'utf8');
  const { html, title, headings } = renderMarkdown(markdown, page.source, root);
  writeFileSync(
    out + page.output,
    layout({
      title,
      body: html,
      current: page.output,
      toc: headings.filter((h) => h.depth === 2),
      sourceUrl: `${repoUrl}/blob/main/${page.source}`,
    }),
  );
}

const list = pages
  .map(
    (p) =>
      `<li><a href="${p.output}">${escapeHtml(p.nav)}</a><p>${escapeHtml(p.summary)}</p></li>`,
  )
  .join('\n');
writeFileSync(
  out + 'index.html',
  layout({
    title: 'Wardlock documentation',
    body: `<h1>Wardlock documentation</h1>
<p>Wardlock is a free, open-source, self-hostable way to publish static sites with access control built in. Deploy a folder to a subdomain with one command, then decide who can open it: anyone, people holding a share link, people who know a password, people who can receive an email code at an allowed address, or people who sign in with GitHub, Google or your own OIDC provider.</p>
<p class="notice">Wardlock is alpha software and has not had an external security review. Do not use it to protect anything sensitive yet.</p>
<ul class="pages">
${list}
</ul>
<p>The source, roadmap and contributor guides are in the <a href="${repoUrl}">GitHub repository</a>.</p>`,
  }),
);
writeFileSync(
  out + '404.html',
  layout({
    title: 'Page not found',
    body: '<h1>Page not found</h1>\n<p>There is no page at this address. Start from the <a href="index.html">documentation home</a>.</p>',
  }),
);
copyFileSync(new URL('style.css', import.meta.url), out + 'style.css');
console.log(`docs-site: wrote ${pages.length + 2} pages to docs-site/dist`);

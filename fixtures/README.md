# Fixtures

Synthetic sample sites for tests. Everything here is invented: no real people, organisations, email addresses, tokens or customer content, ever.

- `sites/basic/`: a small static site exercising index files, clean URLs (`guide.html`), a custom `404.html`, nested directories, an asset and a script with precompressed `.br` and `.gz` siblings. C1.3 (serving) and A2.6 (renditions) build their tests on it and may add sites alongside it.
- `sites/spa/`: a single-page app with no `404.html`, served both with and without the SPA fallback.
- `sites/renditions/`: pages exercising the rendition conversion contract (SPEC "AI-readable renditions"): headings, nested and numbered lists, tables, code blocks, links with share-link tokens to strip, images with and without alt text, escaping, and scripts, styles, forms and hidden content to drop. Its tokens are fake. Golden renditions of every fixture page are in `apps/server/internal/deploys/testdata/renditions/`; regenerate them with `go test ./internal/deploys -run TestRenditionGoldens -update` from `apps/server` and review the diff.

`assets/app.js.gz` is `gzip -9 -n` of `app.js`; `assets/app.js.br` is a valid Brotli stream holding `app.js` in one uncompressed meta-block (no Brotli encoder is needed to regenerate it). Keep the siblings in step with `app.js`.

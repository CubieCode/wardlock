# Fixtures

Synthetic sample sites for tests. Everything here is invented: no real people, organisations, email addresses, tokens or customer content, ever.

- `sites/basic/`: a small static site exercising index files, clean URLs, a custom `404.html`, nested directories and an asset. C1.3 (serving) and A2.6 (renditions) build their tests on it and may add sites alongside it.

The built-in hello-world page served at `hello.<base domain>` in F0.1 is embedded in the server (`apps/server/internal/edge/hello/`), not loaded from here.

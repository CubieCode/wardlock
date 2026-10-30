# Wardlock docs site

A static site generated from the user-facing pages of [`docs/`](../docs), meant to be served publicly by a Wardlock instance with `wardlock deploy`.

## Pages

| Source                   | Page                  |
| ------------------------ | --------------------- |
| `docs/SPEC.md`           | `spec.html`           |
| `docs/DEPLOYMENT.md`     | `deployment.html`     |
| `docs/SECURITY-MODEL.md` | `security-model.html` |
| `docs/ACCEPTABLE-USE.md` | `acceptable-use.html` |

The build also writes `index.html`, `404.html`, `style.css` and `site.js`, a few lines that open any collapsed `<details>` block around the target of a `#fragment` link so links into folded sections land on visible text (the pages work without it). `docs/ARCHITECTURE.md`, `docs/MODULES.md` and `docs/DESIGN-SYSTEM.md` are contributor documentation and stay in the repository; links to them, and to any other file in the repository, become links to GitHub. To publish another page, add it to `pages` in `src/render.mjs`.

## Build and link check

```sh
pnpm docs:build
```

This writes `docs-site/dist` and then checks every link in it without network access: each relative link must name a built file and each `#fragment` an ID in its target page. While rendering, every relative link in `docs/` must point to a published page or to a file that exists in the repository. Heading IDs follow GitHub's rules, so anchors written for the repository work on the site; headings inside `<details>` blocks get IDs too, and the table of contents lists only `##` headings, which the docs keep outside those blocks. `pnpm build` and `pnpm check` include this build, and the `Docs site` workflow runs it on every pull request (nothing is uploaded; the deploy job builds the site itself).

## Deploy

The `Docs site` workflow's deploy job publishes the site on every push to `main` that changes `docs/` or `docs-site/`, and on a manual run from `main`, through this repository's deploy action ([`actions/deploy`](../actions/deploy/README.md)) with the CLI built from source. It deploys to the instance in the `WARDLOCK_SERVER` repository variable with the `WARDLOCK_TOKEN` secret, an API token with the deploy scope only. Pull requests only build, unless the repository variable `DOCS_PREVIEWS` is `true`: then a pull request from a branch of this repository also gets the preview `docs--pr-<number>` with a comment giving its address, deleted when the pull request closes. Set it only once that instance runs a release with preview deploys (C1.19). To deploy by hand instead, with the CLI logged in to the target instance:

```sh
pnpm docs:build
cd docs-site
wardlock deploy --server https://wardlock.example.com
```

`wardlock.json` supplies the site name (`docs`) and the directory (`dist`). The site is public: a new site has no `/` policy, and none should be set on it.

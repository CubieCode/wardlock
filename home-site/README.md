# Wardlock home site

The page `wardlock.nz` serves, deployed with `wardlock deploy` to a Wardlock instance as the site `home`. It carries the `go-import` tags for the Go vanity import paths (`wardlock.nz/cli`, `wardlock.nz/server`, `wardlock.nz/apiclient`; see [DEPLOYMENT "Go import paths"](../docs/DEPLOYMENT.md#go-import-paths)) and sends browsers on to the [documentation](https://docs.wardlock.nz/).

| File                | Purpose                                                   |
| ------------------- | --------------------------------------------------------- |
| `public/index.html` | the page: one `go-import` tag per module, then a redirect |
| `wardlock.json`     | site `home`, directory `public`, SPA fallback on          |

`pnpm check` compares the tags with the modules in `go.work` (`scripts/home-site.test.mjs`), and the release workflow runs `go install` through this page (`scripts/test-module-tags.sh`). The `Home site` workflow deploys it from `main` when it changes.

<details>
<summary>Why the SPA fallback, and how the domain was attached</summary>

Go looks up the full package path, such as `https://wardlock.nz/cli/cmd/wardlock?go-get=1`, and needs a page with status 200 whose `go-import` tag names a prefix of it. With the SPA fallback, every path without a file extension answers `index.html` with status 200, so one page serves every package of all three modules; Go picks the tag whose module path matches. The query string is ignored.

The custom domain is attached once, by hand, with a token that has the manage scope (the workflow's token has the deploy scope only):

```sh
wardlock domain add home wardlock.nz
```

`wardlock.nz` is an apex domain, so it cannot have a CNAME: it has A and AAAA records pointing at the server and the `_wardlock` TXT record that `domain add` prints, all DNS only. Check it with `curl -s 'https://wardlock.nz/cli/cmd/wardlock?go-get=1' | grep go-import`.

</details>

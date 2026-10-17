# Product specification

This is the intended v1 behaviour. Built so far: F0.1 (the server serves a dashboard placeholder, `/healthz`, `/.wardlock/health` and `GET /api/v0/health`; the CLI has `version` and `help`), C1.1 (owner bootstrap, email-code sign-in, invitations and dashboard sessions in the API and `wardlockd admin create`), C1.2 (the deploy API: sites, manifests, blob upload, activation, rollback and teardown), C1.3 (the edge serves each site's active deploy; see Serving), A2.1 (policies, their enforcement at the edge and the policy API; see Policies) and D3.1 (the dashboard's signed-in shell, email-code sign-in and sign-out, the `/setup` and `/sign-in?code=` pages, and session expiry; see Dashboard). Sites are public unless their owner protects them; no sign-in method exists yet (share links arrive with A2.2), so a protected path cannot be opened until then. Each section names the roadmap item that delivers it. Where a rule is a proposal awaiting confirmation, it says so; items must not silently change these rules.

## Terms

- **Instance:** one running `wardlockd` with its data directory.
- **Owner:** an account that can sign in to the dashboard and the API. Every site belongs to exactly one owner.
- **Site:** a named place content is published to, reachable at `<name>.<base domain>` and optionally at verified custom domains.
- **Deploy:** an immutable snapshot of a site's files. One deploy per site is active.
- **Policy:** the rule deciding who may read a site or path.
- **Share link:** a URL carrying a secret token that satisfies a site's policy.
- **Visitor:** anyone (person, bot or AI tool) requesting a site.

## Tokens

Share links and API tokens use one format: `wl_<public id>_<secret>`. The public ID is 10 lowercase base32 characters and names the token in logs, the dashboard and the access log. The secret is 256 random bits, base64url-encoded. The server stores only an HMAC-SHA-256 of the whole token keyed by `WARDLOCK_SECRET`, so a raw token is visible exactly once, at creation or rotation. Details: [wardlock-access](../.agents/skills/wardlock-access/SKILL.md) and [SECURITY-MODEL](SECURITY-MODEL.md).

## Owners and sign-in (C1.1, C1.6, C1.7)

- An instance has one or more owners. There is no open sign-up, ever, in v1: owners are invited by an existing owner or created by the operator.
- **Bootstrap.** When no owner exists, `wardlockd serve` prints a one-time setup URL to the log (`https://<dashboard host>/setup?code=…`, single use, valid 60 minutes, reprinted on each start until used). Alternatively the operator runs `wardlockd admin create --email you@example.com`, which creates the owner offline and prints a one-time sign-in URL.
- **One-time URL paths** on the dashboard host, which the dashboard (D3.x) must serve: `/setup?code=<code>` (the setup URL; the page asks for the owner's email and calls `completeSetup`) and `/sign-in?code=<code>` (admin-create and invitation links; the page calls `redeemSignInLink`). Both use `https://<dashboard host>` without a port, except a `localhost`/`*.localhost` dashboard host, which uses `http://` and the listen port.
- **Invitations.** An owner invites another by email address; the invitee gets a one-time sign-in link valid for 7 days and can afterwards sign in by email code. The response is the same whether or not the address already belongs to an owner (who then gets nothing). The admin-create sign-in URL is valid for 60 minutes.
- **Dashboard sign-in** uses a one-time email code (6 digits, valid 10 minutes, 5 attempts, rate-limited) or a passkey (C1.6). There are no owner passwords. Sending a new code retires the previous one. Default limits: 5 sends per address and 20 per IP per hour, 30 verifications per IP per 15 minutes, 10 setup or sign-in-link redemptions per IP per 15 minutes, 20 invitations per owner per hour; over a limit the API answers `429` with `Retry-After`.
- **Owner sessions** are `__Host-` cookies on the dashboard host, `HttpOnly`, `Secure`, `SameSite=Lax`, idle timeout 14 days, absolute 90 days, revocable from the dashboard.
- **API tokens** (C1.7) are scoped (`deploy`, `manage`), named, revocable, optionally expiring, and shown once. They use the same format and hashing as share-link tokens (see Tokens).
- **CLI login** (C1.7): `wardlock login --server URL` starts a device-code style flow. The CLI shows a short user code and opens the browser at `https://<dashboard host>/cli-login`; the signed-in owner confirms the code and chooses scopes; the CLI polls until approved and receives an API token. The token is stored in the OS keychain where available (macOS Keychain, Windows Credential Manager, Secret Service on Linux), otherwise in `~/.config/wardlock/credentials.json` with mode 0600. `wardlock logout` deletes it locally and revokes it on the server.

## Dashboard (D3.1)

- Signed-out visitors to any dashboard page are sent to `/sign-in?next=<path>` and returned to that path (same origin only) after signing in.
- `/sign-in` asks for an email address, sends a code and then asks for it; a wrong, expired or exhausted code says so beside the field and offers a new code. The page says a code was sent "if the address belongs to an owner", matching the API's non-revealing answer.
- `/sign-in?code=` and `/setup?code=` read the one-time code once and remove it from the address bar and history; a used or expired code explains what to do next.
- The dashboard rechecks the session when the window regains focus and treats any `401` from an owner call as an ended session (expired, revoked or signed out elsewhere): it returns to sign-in with "Your session has ended" and comes back to the same page afterwards.
- The signed-in shell has a sidebar (a top bar with a Menu button below 48rem) listing only built screens, the signed-in owner's address and Sign out.

## Sites (C1.2)

- Site names are DNS labels: 1–63 characters of `a-z`, `0-9` and `-`, not starting or ending with `-`. Reserved names: `auth`, `www`, `api`, `admin`, `app`, `dashboard`, `mail`, `wardlock`, anything starting with `xn--`, and, when the dashboard host sits under the base domain, the label directly under the base domain on the way to the dashboard host (`dash` for `dash.<base domain>` or `a.dash.<base domain>`).
- A site is created by its first deploy (the CLI creates it before uploading) or from the dashboard. Names are unique per instance; a name held by another owner's site is reported as taken (site names are public through DNS).
- **Custom domains** (C1.9): a site can also be served at domains its owner controls; see Custom domains.
- **Teardown** deletes the site, its deploys, links, policies and custom domains, and frees the name. Blobs no longer referenced are removed by garbage collection (C1.8).

## Custom domains (C1.9, D3.5)

The goal is that adding one DNS record is all an owner has to do. The record names the site itself, so it works wherever Wardlock is hosted and proves which site the domain belongs to.

- **Adding.** An owner adds a hostname to one of their sites from the dashboard (D3.5) or `wardlock domain add` (C1.9). Hostnames are stored lowercase, as A-labels (punycode), with no trailing dot or port, at most 253 characters. Rejected: IP addresses, wildcards, the base domain and anything under it, the dashboard host, and the auth origin. A hostname belongs to at most one site on the instance; adding one already attached to any site fails with `domain_taken`, without saying which site or owner holds it.
- **Records shown.** Wardlock answers with the records to create:
  - **Routing:** `CNAME <hostname> → <name>.<base domain>`. That target already points at the server through the sites wildcard, so it needs no knowledge of the server's address.
  - **Verification fallback:** `TXT _wardlock.<hostname> → wardlock-verify=<token>`, a random token unique to this claim (a new claim after removal gets a new token). It is needed only when the CNAME cannot be seen: apex domains, which cannot have a CNAME and instead use A/AAAA records (or the provider's ALIAS/ANAME/flattening) pointing at the server; and CDN-proxied records such as Cloudflare's orange cloud, which hide the CNAME.
- **Verification.** A hostname is verified when resolving it follows a CNAME chain that reaches `<name>.<base domain>`, or when `_wardlock.<hostname>` has the claim's TXT token. Lookups go to the resolvers in `WARDLOCK_DNS_RESOLVERS` (default: the system resolver) with caching disabled where the resolver allows it. A new hostname is checked straight away, then with backoff (1 minute growing to 1 hour) for 48 hours; after that it stays `pending` until the owner asks to verify again. The CLI and dashboard show the last check time and a plain reason when it fails (no record, CNAME points elsewhere, TXT token mismatch, DNS error).
- **Statuses.** `pending` (added, not yet verified), `active` (verified: served and eligible for a certificate), `lapsed` (was active, has failed re-checks for 72 hours in a row: no longer served and no certificate issued or renewed). Active and lapsed hostnames are re-checked every 24 hours; a lapsed hostname that passes again becomes active without owner action.
- **Serving** (C1.3). An active hostname serves its site exactly as `<name>.<base domain>` does, with the same policies. Site sessions are host-only, so a visitor signs in separately on each of a site's hostnames. Share links work on any of the site's active hostnames. Pending and lapsed hostnames get the same response as an unknown host.
- **Certificates** (C1.4). In automatic TLS mode, on-demand TLS issues and renews only for active hostnames. Behind a proxy, the proxy issues certificates and asks Wardlock first through `/.wardlock/tls-ask?domain=<hostname>` (see Reserved path), which answers 200 for an active hostname and 404 otherwise.
- **Removing.** Removing a hostname stops serving it immediately and deletes its certificate from Wardlock's storage. The owner should also delete the DNS record: a CNAME left pointing at `<name>.<base domain>` can be claimed by whoever later holds that site name (see SECURITY-MODEL).
- Not in v1: redirecting between a site's hostnames (a "primary" domain), wildcard custom domains, and HTTPS for custom domains in local mode.

## Deploys and rollback (C1.2)

- A deploy uploads a directory. The client sends a manifest of `{path, sha256, size}`; the server replies with the hashes it does not yet hold for that owner (each owner uploads a file once, even if another owner already published the same content); the client uploads only those; then the client activates the deploy. Unchanged files are never re-uploaded. The protocol is in ARCHITECTURE "Deploy upload protocol" and the operations in `packages/api-spec/openapi.yaml`.
- Activation is atomic: a visitor sees either the previous deploy or the new one, never a mix.
- Every deploy stays in history with its time, file count, total size and an optional message. **Rollback** reactivates any earlier deploy instantly, with no upload.
- Default limits (configurable): 25 MiB per file, 20,000 files and 1 GiB per deploy, 50 deploys of history per site (older ones are pruned, never the active one).
- Paths are normalised to forward slashes, must be valid UTF-8, must not contain `..`, empty segments, control characters or invisible format characters (Unicode Cf, such as zero-width spaces and bidi overrides), and must not start with `.wardlock/`. Such deploys are rejected with the offending path named. The server keeps paths byte for byte without Unicode normalisation; the CLI (C1.5) sends them in NFC.

## Serving (C1.3)

- `/` and `/dir/` serve `index.html`; `/page` serves `page.html` when `page` does not exist (clean URLs); `/page.html` is served as is. `/dir` without its slash redirects (301, to the absolute path, query kept) to `/dir/` when `dir/index.html` exists and neither `dir` nor `dir.html` does. Only `GET` and `HEAD` are served (405 otherwise).
- A missing path serves the deploy's `404.html` with status 404 if present, otherwise a plain 404. With the **SPA fallback** option (recorded on each deploy from `--spa` or `wardlock.json`, so rollback restores it), missing paths without a file extension serve `/index.html` with status 200.
- Responses carry `ETag` (content hash) and `Last-Modified` (activation time), honour conditional requests and byte `Range` requests, and serve precompressed `.br` or `.gz` siblings when the client accepts them: the sibling with the highest `Accept-Encoding` q wins, `br` on a tie; `q=0`, or a q that does not parse or lies outside 0–1, refuses a coding. A file with a sibling answers with `Vary: Accept-Encoding`, and a sibling has its own `ETag`. A precompressed sibling honours a single byte range; a multi-range request for one is answered 200 with the whole sibling. `404.html` is sent whole, without conditional or range handling. A file whose extension has no known type is sent as `application/octet-stream`, never sniffed.
- Cache headers: `Cache-Control: public, max-age=0, must-revalidate` for every deployed file; `private` replaces `public` on protected sites. Not built yet: `public, max-age=31536000, immutable` for files under `/assets/` with hashed names, which needs a per-site opt-in that a later roadmap item must add.
- An unknown host, a nested subdomain, a pending or lapsed custom domain and a site with no active deploy all get the same plain 404, for every method and path, `/.wardlock/` included. Deploy files under `.wardlock/` (in any letter case) are never served, even as `404.html`.
- `/.wardlock/` is reserved on every site (see Reserved path).

## Policies (A2.1)

- Each site has a **default policy** (path `/`), which cannot be deleted. Owners may add **path policies** for directory prefixes (`/docs/`, `/internal/`): they start and end with `/`, have no empty, `.` or `..` segments, and can never cover `/.wardlock/`.
- The policy for a request is the one with the **longest matching prefix**, falling back to the site default. Policies do not stack: a public `/assets/` under a protected site is public. A prefix also governs its directory's path without the slash (`/docs/` governs `/docs`), and matching is case-sensitive on the decoded, dot-resolved request path. This errs on the strict side: with a protected default and a public `/docs/`, the clean URL `/docs` (which may answer with `docs.html`) is allowed by `/docs/` but its file is checked against the default, so a visitor without a session gets a plain 404 there, while `/docs/` itself is public. Conversely, with a protected `/docs/` under a public default, `/docs` asks for sign-in even when it would answer with the public `docs.html`, which stays reachable at `/docs.html`. A file that answers a request from another path (`404.html`, the SPA `index.html`, a clean URL's `page.html`) must also be allowed by its own path's policy, or the request gets a plain 404 instead of its content.
- A policy is either **Public** or **Protected**. A protected policy enables one or more **methods**: share links, password, email code, OAuth. A visitor is allowed if they satisfy **any** enabled method (methods are alternatives, not layers).
- Share links are enabled on every protected policy by default and can be switched off per policy.
- **Decision (A2.1):** a new site's default policy is **Public** with no methods, and so is the default of a site created before A2.1 (it is stored on first use). Owners opt into protection per site or per path. The reason: deploys keep working as before, and protecting a site is only useful once a way in exists (share links from A2.2, then password, email code and OAuth), so protection is opt-in rather than something every new site starts behind.
- Each policy has a **version**, starting at 1 and increased by every change to it (setting identical settings changes nothing). Changing a policy takes effect on the next request; a site session is bound to the policy and version it was created under, so a change ends every session of that policy, and deleting a path policy ends its sessions.
- A request a protected policy does not allow gets `401`: browsers (requests that accept `text/html`) see the login page at the same URL, other clients a short plain-text body with `WWW-Authenticate: Bearer`. Neither says whether the path exists.
- Owners read and set policies through the API (`listPolicies`, `setPolicy`, `deletePolicy`); `wardlock policy set` follows once the CLI can authenticate (C1.7).
- Owners signed in to the dashboard are not automatically signed in to sites (they are on another domain). The dashboard's "Open site" action creates a short-lived (10 minute) share link for the owner.

## Share links (A2.2)

A share link is `https://<site host><path>?t=<token>`.

- **Opening:** a valid token serves the page directly with status 200 in the same response that sets a site session cookie. There is no login screen and no redirect. In HTML responses only, a small inline script removes `t` from the address bar with `history.replaceState`, so it is not bookmarked or shared by accident; the session cookie keeps later navigation working.
- **No-cookie clients:** AI fetch tools, `curl` and link-preview bots keep no cookies and run no JavaScript. Because the token is checked on every request that carries it, they get the content on every request. `Authorization: Bearer <token>` is accepted as an alternative to `?t=`.
- **Expiry:** at creation choose a duration (1 hour, 1 day, 7 days, 30 days, custom) or **never**. Never-expiring links are a first-class choice for permanent references in tools like Linear.
- **Use limit:** optionally limit the number of uses (1 for one-time links). Use-limited links show a "Click to view" interstitial on GET; the use is counted only when the visitor presses the button (a POST), so preview bots and email scanners cannot spend it. After the last use the link stops working.
- **Label:** free text up to 200 characters ("Linear ENG-123", "sent to Alice") shown in the dashboard and access log. Labels are never part of the URL.
- **Scope:** a link is bound to one site and optionally a path prefix; it grants nothing elsewhere.
- **Revoke:** immediate. The token and any session created from it stop working on the next request.
- **Rotate:** issues a new token for the same link (same label, scope, expiry and use count) and invalidates the old one immediately.
- **Listing:** owners see each link's label, public ID, scope, expiry, uses and last access, never the token after creation.
- **Access log:** every access records the link's public ID, time, IP (truncatable by config) and User-Agent (A2.8).

## Password (A2.3)

- A policy may enable one password per policy (not per visitor). Visitors see a minimal login page at `/.wardlock/login`, then get a site session.
- Passwords are stored with Argon2id. Attempts are rate-limited per IP and per site. Changing the password ends existing password sessions.

## Email code (A2.4)

- A policy may enable email codes with an allowlist of exact addresses and/or domains (`example.com` matches `@example.com` only, not subdomains, unless `*.example.com` is listed).
- The visitor enters their address at `/.wardlock/login`; if it is allowed, Wardlock sends a 6-digit code valid for 10 minutes (5 attempts). The response is the same whether or not the address is allowed, so the allowlist cannot be probed.
- Sends and verifications are rate-limited per IP, per address and per site. Mail goes through SMTP, or the `dev://` sink in development (F0.4).

## OAuth and OIDC (A2.5)

- Providers: GitHub, Google and generic OIDC (issuer URL plus client ID and secret). Allowlists: verified email addresses, email domains, and for GitHub organisations.
- All provider redirects go to the central auth origin `auth.<base domain>`, because providers cannot register wildcard redirect URIs. After sign-in it hands a single-use, short-lived, site-bound code to `https://<site>/.wardlock/callback`, which the edge exchanges for a host-only site cookie. PKCE and `state` are used on every flow.

## Site sessions

- Set by any successful method. Host-only `__Host-` cookie, `Secure`, `HttpOnly`, `SameSite=Lax`, signed and bound to the site, the method, the policy version and (for links) the link.
- Default lifetime 7 days, capped by the link's expiry. A `POST` to `/.wardlock/logout` ends it and clears the cookie; it must come from a page on the same host (`Origin` naming the host, or without `Origin`, `Sec-Fetch-Site: same-origin`), so another site, a sibling site included, cannot sign visitors out (`403` otherwise).
- The cookie is `__Host-wardlock_site`, a value signed with an HMAC of the server key that names the site, the policy, its version and the expiry, plus a 256-bit session token whose HMAC is the only copy stored. A session satisfies only the policy it was created under, and only while that policy enables the session's method. A tampered, forged, expired, revoked or stale cookie, or one from another site, is ignored and cleared. One cookie per site host: signing in under another policy replaces it.

## AI-readable renditions (A2.6)

- At deploy time Wardlock converts every HTML file to Markdown and to plain text and stores both alongside the deploy. Conversion keeps headings, paragraphs, lists, tables, links (absolute URLs, without tokens), code blocks and image alt text, and drops scripts, styles, navigation marked `aria-hidden` and forms.
- `?format=md` serves the Markdown (`text/markdown; charset=utf-8`), `?format=txt` the text (`text/plain; charset=utf-8`). Both go through the same policy check as the HTML, so `?t=…&format=md` works without cookies.
- HTML responses include `Link: <…?format=md>; rel="alternate"; type="text/markdown"`.
- Non-HTML files ignore `format`. The exact conversion contract and its fixtures are part of A2.6.

## Access log (A2.8)

Every request for a published site's content (after the access decision, allowed or refused) records one entry: time, site, host, path without query string, status, how it got in (`public`, `link`, `password`, `email`, `oauth`, or `none` when refused), the share link's public ID when it came through a link, the client IP truncated to a configurable prefix (IPv4 /24 and IPv6 /48 by default, or not at all) and the User-Agent. Tokens are never stored: a token in the path or User-Agent is reduced to its public ID. `/.wardlock/` pages, health checks and the dashboard are not logged. Entries are kept for a configurable number of days (90 by default, or forever) and outlive the site they name. Owners query a site's entries, or one share link's, newest first; the dashboard and API views arrive with the screens that show them. Logging never slows serving: under database pressure entries are dropped and counted. Configuration: [DEPLOYMENT](DEPLOYMENT.md#access-log).

## Reserved path

`/.wardlock/` (matched in any letter case, as deploy validation rejects it in any case) on every published site is served by Wardlock and can never be overridden by a deploy: `login`, `callback`, `logout`, `view` (the use-limited interstitial), `health` and `tls-ask` (C1.9: answered only in behind-a-proxy mode and only to requests from a trusted-proxy address, on any `Host`; 404 otherwise). F0.1 implements `health`; since C1.3 it answers only on sites with an active deploy (a host with nothing published is an unknown host). A2.1 adds `login` (GET, the login page for the site default; it lists sign-in methods as A2.3 to A2.5 add them) and `logout` (POST). Policies never apply under `/.wardlock/`.

## Protected-site response headers

`X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer`, `Cache-Control: private` and `Vary: Cookie` on every response a protected policy governs, including its 404s and redirects; the login page and 401 send `no-store` and also `Vary: Accept`, since browsers get the page and other clients the bare 401. Every `/.wardlock/` page sends all three headers with `Cache-Control: no-store` whatever the policy, and the login page also sends a restrictive `Content-Security-Policy` and `X-Frame-Options: DENY`. Wardlock never blocks AI fetchers in `robots.txt`; access is the policy's job.

## CLI (C1.5, C1.7, C1.9)

Global flags: `--server URL` (default from `wardlock.json`, then `WARDLOCK_SERVER`, then the server saved at login), `--json` (machine-readable output, no prompts, for CI), `--help`. Exit codes: 0 success, 1 failure, 2 usage error. Without a TTY or with `--json`, the CLI never prompts. `WARDLOCK_TOKEN` overrides stored credentials for CI.

| Command                                                                                                                      | Behaviour                                                                                                                                                                                                       |
| ---------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `wardlock login [--server URL]`                                                                                              | Browser login described above.                                                                                                                                                                                  |
| `wardlock logout`                                                                                                            | Revoke and forget the stored token.                                                                                                                                                                             |
| `wardlock deploy [DIR] [DOMAIN] [--message M] [--spa]`                                                                       | Upload DIR (default from `wardlock.json`, else `.`) to DOMAIN (`docs`, `docs.<base domain>` or a custom domain). Shows progress (files hashed, uploaded, bytes) and prints the URL. Creates the site if needed. |
| `wardlock list [DOMAIN]`                                                                                                     | Without DOMAIN, the owner's sites with policy and last deploy; with DOMAIN, that site's deploys.                                                                                                                |
| `wardlock rollback DOMAIN [DEPLOY_ID]`                                                                                       | Reactivate the given deploy, or the previous one.                                                                                                                                                               |
| `wardlock teardown DOMAIN [--yes]`                                                                                           | Delete the site after confirmation (`--yes` required without a TTY).                                                                                                                                            |
| `wardlock share create DOMAIN [--path P] [--label L] [--expires 7d\|never] [--uses N]`                                       | Create a link and print its URL once.                                                                                                                                                                           |
| `wardlock share list DOMAIN`                                                                                                 | Links with label, public ID, scope, expiry, uses, last access.                                                                                                                                                  |
| `wardlock share revoke DOMAIN LINK_ID`                                                                                       | Revoke by public ID.                                                                                                                                                                                            |
| `wardlock share rotate DOMAIN LINK_ID`                                                                                       | Rotate and print the new URL once.                                                                                                                                                                              |
| `wardlock policy set DOMAIN [--path P] (--public \| --protected [--links] [--password] [--email ADDR,…] [--oauth PROVIDER])` | Set the default or a path policy. Passwords are read from a prompt or stdin, never a flag.                                                                                                                      |
| `wardlock domain add DOMAIN HOST [--wait]`                                                                                   | Attach custom hostname HOST to the site DOMAIN and print the records to create. `--wait` re-checks until HOST is active or 10 minutes pass.                                                                     |
| `wardlock domain list DOMAIN`                                                                                                | The site's custom hostnames with status, last check and failure reason.                                                                                                                                         |
| `wardlock domain verify DOMAIN HOST`                                                                                         | Check HOST's DNS now and print the result.                                                                                                                                                                      |
| `wardlock domain remove DOMAIN HOST [--yes]`                                                                                 | Detach HOST after confirmation (`--yes` required without a TTY) and remind the owner to delete the DNS record.                                                                                                  |
| `wardlock version [--server URL]`                                                                                            | Built in F0.1.                                                                                                                                                                                                  |

Only built commands appear in `--help`.

## `wardlock.json` and `.wardlockignore` (C1.5)

`wardlock.json` in the directory you run the CLI from:

```json
{
  "server": "https://wardlock.example.com",
  "site": "docs",
  "dir": "dist",
  "spa": false
}
```

All fields are optional; flags override them. Unknown fields are an error, so typos are caught.

`.wardlockignore` uses `.gitignore` syntax, relative to the deployed directory. Always ignored: `.git/`, `node_modules/`, `.DS_Store`, `wardlock.json`, `.wardlockignore`, `.env*`. Other dotfiles are ignored unless listed with `!`, except `.well-known/`, which is included.

## Not in v1

Teams and shared ownership, an MCP server, hosted service features (billing, abuse handling), Postgres or S3 backends, multiple server instances. See ROADMAP "Not planned yet".

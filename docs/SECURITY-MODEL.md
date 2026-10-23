# Security model

Wardlock protects static content with secrets that travel in URLs. That is a deliberate trade-off: a share link must open instantly for people, `curl`, AI fetch tools and link-preview bots that keep no cookies. This document lists what we defend against, how, and what we accept: each threat leads with its defence, and the reasoning, exact limits and accepted risks are collapsed beneath it. Implementation rules live in [wardlock-access](../.agents/skills/wardlock-access/SKILL.md); A2.7 checked the implementation against this document.

## Assets and actors

- **Assets:** site content behind non-public policies; share-link tokens, API tokens, passwords and email codes; owner accounts; the server key (`WARDLOCK_SECRET`); the instance's reputation and domain.
- **Actors:** owners; intended visitors; unintended recipients of a forwarded link; automated fetchers (AI tools, preview bots, email scanners); an attacker on the internet; a malicious site owner on a shared instance; anyone running a public instance.

## Threats and mitigations

### Token leakage

Raw tokens are shown once, stored only as HMACs, never logged by Wardlock and stripped from the address bar and `Referer`; rotation and revocation take effect on the next request. Sharing a link with a tool is sharing the content, so use expiring or use-limited links for anything you may want back.

<details>
<summary>Each leak path, its mitigation and residual risk</summary>

| Path                                                      | Mitigation                                                                                                                                                                                                       | Residual risk                                                                                                       |
| --------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `Referer` header to third parties                         | `Referrer-Policy: no-referrer` on protected sites; the token is stripped from the address bar on HTML pages                                                                                                      | A site's own JavaScript can still read and send `location` before the strip runs; site owners control their scripts |
| Browser history and bookmarks                             | `history.replaceState` removes `t` after load; the session cookie keeps navigation working                                                                                                                       | Clients without JavaScript keep the full URL                                                                        |
| Server and proxy logs                                     | Wardlock never logs raw tokens (`platform.RedactURL`, `platform.Secret`); docs tell operators to redact `t` in reverse-proxy logs                                                                                | Third-party proxies the operator does not configure                                                                 |
| AI providers and preview services caching fetched content | Accepted: sharing a link with a tool is sharing the content. Use expiring or use-limited links, and revoke when done. `X-Robots-Tag: noindex` and `Cache-Control: private` discourage indexing and shared caches | Providers may retain content under their own policies                                                               |
| Shoulder-surfing, screenshots                             | Dashboard shows the token once; afterwards only the label and public ID                                                                                                                                          | None beyond normal care                                                                                             |
| Database theft                                            | Only HMAC-SHA-256 of tokens (keyed by `WARDLOCK_SECRET`) and Argon2id password hashes are stored                                                                                                                 | Stolen database plus stolen key allows offline token checks, not recovery                                           |

Rotation and revocation take effect on the next request, including for sessions created from the link. A session is stored only while its link still has the token that was checked, so a request racing a rotation or revocation cannot leave a live session behind.

**Exception: clean-URL redirects keep the token.** wardlock-access forbids putting a raw token in a URL the server builds. The one exception is the clean-URL redirect (`/dir` to `/dir/`, SPEC "Serving"), whose `Location` keeps the request's query, `t` included, so AI fetchers and other cookie-less clients that follow it still get the page. The token is the one the client just sent, the redirect points to the same host, and protected responses send `Referrer-Policy: no-referrer` and `Cache-Control: private`; the residual risk is a proxy or client that logs `Location` headers but not request URLs.

</details>

### Third-party log output

Wardlock's own log lines follow the rules above; libraries that log through it are held to them as follows (A2.10). Each adapter may log only what is listed here: ACME and TLS output passes a field allowlist with free text cut and scrubbed, DNS providers and the SMTP client log no secrets or message text, the `dev://` mail sink logs recipients but never bodies or codes, and Go's HTTP server logs connection errors without request contents.

<details>
<summary>What each library may and may not log</summary>

- **certmagic and acmez (automatic TLS).** Their zap and slog output passes an allowlist of field names in `internal/tls/log.go`; any other field is dropped and only its name is kept, in `dropped`. They may log hostnames and the DNS zone, the CA's directory, order, authorisation, challenge and certificate URLs, challenge types and states, the method and status of each ACME request, certificate hashes, counts, times and durations, and free text in `error` and ACME problem fields. Free text is cut to 512 bytes, with email addresses, `Bearer` and `Token` credentials and PEM private keys replaced, because a DNS provider's error can carry its whole response body and certmagic's errors name storage paths that contain the ACME contact address. They never log the ACME account key or a certificate key, key authorisations, HTTP-01 challenge tokens or paths, DNS-01 record values, ACME request or response headers (`Authorization`, nonces), the ACME contact address, storage keys, client addresses, or external account binding values (Wardlock configures none; such fields would be dropped). An allowlist rather than a denylist, because these libraries log at debug level, add fields between releases, and a field nobody has reviewed should stay out until someone has; the cost is that a useful new field is missing until it is added. A test captures the debug output of a real issuance against an in-process ACME CA and checks it for every key, key authorisation, record value, header and the contact address.
- **DNS providers (libdns Cloudflare and deSEC).** They have no logger; their errors reach the log only through certmagic's `error` field, treated as above. The API token is configuration, printed only as `[redacted]`.
- **SMTP client.** Failures are logged by SMTP stage and reply code only (see Brute force and enumeration).
- **The `dev://` mail sink.** Each message's recipient, subject and file name, never the body or code. It is the default mail mode, so the server warns at startup when it runs outside local mode: such an instance delivers nothing and logs visitor addresses.
- **Go's HTTP server.** Connection-level errors, such as failed TLS handshakes, at warning level with the client's address and port, never request contents.

</details>

### The server key

`WARDLOCK_SECRET` keys every stored token, code and session hash and signs site session cookies. Treat it as a long-lived secret and change it only to recover from a leak.

<details>
<summary>What changing it ends</summary>

There is no key rotation yet: changing it ends every owner (dashboard) session and every site session at once, and, from A2.2 and C1.7, invalidates every share link and API token.

</details>

### Cookie tossing and cross-site session abuse

Site cookies use the `__Host-` prefix and are signed and bound to one site and policy version, protected responses refuse to be embedded by other origins, and production instances should serve sites from a different registrable domain from the dashboard.

<details>
<summary>Why and how: cookies, logout and caching</summary>

All sites share one parent domain, so a page on `evil.<base>` could set a cookie with `Domain=<base>` that the browser then sends to `docs.<base>`. Wardlock's site cookies use the `__Host-` prefix, which browsers only accept when the cookie is host-only, `Secure` and `Path=/`, so a sibling cannot forge or overwrite them, and Wardlock ignores any session cookie without the prefix. Cookies are signed and bound to one site, so a cookie copied from one site fails on another (and is cleared there). `/.wardlock/logout` only ends a session for a same-origin request, so a sibling site cannot sign a visitor out of another site. Protected responses send `Vary: Cookie` so no shared cache hands one visitor's page to another, and any response that sets or clears a cookie sends `Cache-Control: no-store`, whatever it carries (a file, a redirect, a 404). The signature also binds the cookie to one policy and its version, so a policy change or deletion ends its sessions on the next request; the cookie's token is stored only as an HMAC, so revocation (logout, and link revocation from A2.2) is checked against the database on every protected request. The site cookie (`__Host-wardlock_site`) and the dashboard cookie (`__Host-wardlock_session`) have different names, formats and stored hashes (the site cookie is signed under its own MAC purpose; the dashboard cookie is a random token stored only as an HMAC under another purpose), so neither ever authenticates the other.

</details>

<details>
<summary>Embedding by sibling sites (A2.7 decision)</summary>

Sibling sites are same-site to each other, so a site cookie (`SameSite=Lax`) rides along when `evil.<base>` loads a protected page of `docs.<base>` as a script, image or stylesheet: without more, a malicious owner could run another site's protected JavaScript in their own page, or probe whether a visitor has access or a path exists from load and error events. Every response a protected policy governs, and every `/.wardlock/` page, therefore sends `Cross-Origin-Resource-Policy: same-origin`, so browsers refuse to hand it to any other origin. Navigations, form posts and cookie-less fetchers (`curl`, AI tools) are unaffected, and public sites do not send it. The price: a protected site cannot be embedded from another origin, which includes one of its own custom domains loading assets from its `<name>.<base>` host (or the reverse); such a site must load its assets from the host it is served on.

</details>

<details>
<summary>Why a separate sites domain</summary>

Production deployments should serve sites from a **different registrable domain** from the dashboard (for example sites on `example-sites.net`, dashboard on `wardlock.example.com`). Hosted sites run arbitrary JavaScript; keeping them off the dashboard's registrable domain means they are cross-site to it, so `SameSite` protections apply and no site can toss cookies at the dashboard. The single-domain layout is fine for local development and trusted single-owner instances. The [Public Suffix List](https://publicsuffix.org) is an additional option for large public instances.

</details>

### XSS from hosted sites

Wardlock serves owner-supplied HTML and JavaScript as is; it does not sanitise sites. Containment comes from isolation, not filtering: every site is its own origin, and the dashboard and API live on another host.

<details>
<summary>How sites are isolated</summary>

In full: every site is its own origin; site cookies are `HttpOnly` and host-only; the dashboard and API live on a different host (ideally a different registrable domain) with a strict Content Security Policy and `X-Frame-Options: DENY`; `/.wardlock/` pages are rendered by Wardlock, not by the site, and send `Cache-Control: no-store`; the login page also sends a `Content-Security-Policy` that allows no scripts and `X-Frame-Options: DENY`. A deploy can never place files under `/.wardlock/`.

</details>

### Reading content back

Since C1.14 an owner can download the files of any of their deploys through the API (`wardlock pull`). Any API token can therefore read its owner's content as well as publish it: **a leaked `deploy`-scoped CI token now exposes every file of every one of the owner's sites**, protected ones included, not just the ability to publish. Keep CI tokens in the CI system's secret store, give them an expiry, and revoke one that may have leaked. Downloads never hand out another owner's file, a taken-down site's files or a withheld file, and never render on the dashboard origin.

<details>
<summary>How downloads are contained</summary>

- **Scope.** Listing a deploy's files and downloading them are read operations, accepted with the dashboard session or a token with either scope, like every other read. A token that can deploy could already replace a site's content; reading it back adds confidentiality exposure, which is why the advice above changes. Tokens never reach protected content through the edge: a token is an API credential on the dashboard host only, and a share-link token, a site session cookie or the dashboard cookie on a site's host never authenticates the API.
- **Content addressing.** Bodies are stored once per hash and shared across owners, and a deploy may list any hash before it is uploaded. A download is therefore answered only when the deploy lists the hash **and** its owner has uploaded those bytes themselves (`owner_blobs`); otherwise, and for another owner's site or deploy, the answer is the same `404` as for something missing. Guessing a hash and listing it in a pending deploy reveals and returns nothing.
- **The dashboard origin.** Bodies come from the dashboard host, so they must never render there: every body is `application/octet-stream` with `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; sandbox`, plus `Cross-Origin-Resource-Policy: same-origin`, `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex` and `Cache-Control: private, no-store`. A hosted page opened this way is saved, not run, and even a browser that ignored the attachment would run it sandboxed with no scripts.
- **Operator action.** A taken-down site answers `409 site_unavailable` to listing and downloads, for its owner too, and a file content screening withheld answers `451 file_withheld`: operator action preserves content as evidence and must not become a way to retrieve it for rehosting elsewhere. A suspended owner's tokens and sessions are refused outright.
- **The CLI.** `wardlock pull` re-checks every path before writing (no `..`, absolute paths, names the local file system cannot hold, case-folding collisions), writes only through `os.Root` beneath DIR, and verifies each body's SHA-256 before renaming it into place, so a hostile or broken server cannot make it write outside DIR or leave a wrong file under a listed name.

</details>

### Preview bots and email scanners

Chat apps, email security scanners and link unfurlers fetch URLs automatically. For ordinary links that is intended (AI-readability is a feature). For use-limited links it would spend uses, so those links show an interstitial on GET and count a use only on POST. Bots do not press buttons.

<details>
<summary>How the interstitial is protected</summary>

The interstitial's form carries a short-lived ticket signed with the server key and bound to the link's current token, not the token itself, so the page never echoes the token and rotating or revoking the link voids any ticket already shown. The button only works from a page on the same host (the logout rule), so a page elsewhere cannot post a ticket for a link of its own and replace a visitor's session with one it chose.

</details>

### Path policies and fallbacks

Sites are public until their owner protects them (SPEC "Policies"), so protection is a deliberate choice per site or path rather than a default an owner might assume. Fallback files and refusals never reveal or serve protected content under another path's policy.

<details>
<summary>Why and how: coverage, fallbacks and matching</summary>

A path policy covers URL paths, not just files: `/docs/` also governs `/docs`, so its clean-URL and redirect answers sit behind it. Files that answer for another path (`404.html`, the SPA `index.html`, a clean URL's `page.html`) are checked against their own path's policy too, so a public path under a protected site never serves protected content as a fallback. A request a protected policy refuses gets the same 401 whether or not the path exists, so policies do not reveal a protected site's file names. Matching is case-sensitive, like file paths; `/Docs/` is not covered by a `/docs/` policy, and the owner must protect the paths their deploy actually uses.

</details>

<details>
<summary>Decision (C1.10): long-term caching of protected hashed assets</summary>

A deploy that opts into `immutableAssets` (SPEC "Serving") has its hashed files under `/assets/` sent with `Cache-Control: private, max-age=31536000, immutable` on protected sites. They stay `private` with `Vary: Cookie` and the other protected-site headers, so no shared cache stores them, but a visitor's browser keeps what it already fetched for up to a year without asking again: revoking a link, ending a session or changing the policy stops new requests, not copies already held. The owner accepts this by opting in per deploy; pages, renditions, fallbacks and every response that sets or clears a cookie keep their usual headers (`no-store` for the last), and login, interstitial and callback pages are never cached.

</details>

### Brute force and enumeration

Tokens carry 256 bits of secret, so guessing is infeasible; even so, failed token checks are rate-limited per IP and per site, as are password attempts and email-code sends and verifications. Responses never reveal whether a link, address or path exists, and no flood of guesses or of invented addresses lets anyone in, though a determined attacker with six or more networks can hold one site's sign-in method shut (accepted, below).

<details>
<summary>Passwords (A2.3)</summary>

Policy passwords (A2.3) are hashed with Argon2id (19 MiB, 2 passes, 1 lane, 16-byte salt, PHC string); every check derives one key, with a stand-in when the policy has no password, and compares in constant time. Password attempts are limited to 10 per client and 100 per site per 15 minutes (with network shares under pressure, below), counted before the check, and the sign-in form accepts only same-origin posts, so another site cannot sign a visitor into a session of its choosing.

</details>

<details>
<summary>Enumeration, visitor email codes and mail logs (A2.4)</summary>

Responses do not distinguish "no such link" from "wrong token", and the email-code form answers the same for allowed and disallowed addresses: after the address is read and the limits counted (20 sends per client, 5 per address and 200 per site per hour), the allowlist check, code storage and mail run after the response, and code entries look up the stored code whatever the address. Visitor email codes (A2.4) are 6 digits with 5 attempts and a 10-minute lifetime, bound to one policy and its version; only HMACs of the code and the address are stored, attempts are counted in the database before the comparison, and a session is stored only while the policy still enables email codes at the checked version, so an allowlist change that races a sign-in leaves no session. Per-address counts live in a bounded table of their own (100,000 addresses), separate from the per-IP table that password, share-link and email-code IP limits use, because addresses cost nothing to invent: filling it never affects password or share-link sign-in, and addresses it does not hold are counted in its overflow sketch (below), not refused. Two residual risks are accepted: anyone who knows an allowed address can spend its send and verify limits (5 sends an hour, 10 entries per 15 minutes), holding off that visitor's email sign-in for up to one window, and five wrong guesses at a visitor's live code use it up, so the visitor must ask for a new one. Neither lets the attacker in. Mail errors are logged by SMTP stage and reply code only, never with the server's text, which often repeats the recipient. The `dev://` sink, meant for local development, logs each message's recipient and subject (never the code), so a development instance's log shows which addresses were sent codes.

</details>

<details>
<summary>Owner sign-in codes and passkeys (C1.6)</summary>

Owner sign-in codes are 6 digits with 5 attempts and a 10-minute lifetime. Owner passkeys (C1.6) are phishing-resistant: the browser binds each assertion to the dashboard origin and relying party, and the server also checks both, the single-use challenge and the signature counter, refusing every failure with one error. A hosted site cannot use an owner's passkey, since its origin is not the dashboard's; with the dashboard on a sibling of the sites (the single-domain setup), the relying party is still the dashboard host itself, never the shared parent domain. Passkey challenges are held in memory, single use, and lost on restart. Sign-in and registration challenges live in separate bounded tables, with per-network (sign-in) and per-owner (registration) caps on open challenges, so a single client or owner cannot fill either; filling the sign-in table still takes about 2,000 IPv4 addresses or IPv6 /48s, which would delay passkey sign-in (never email-code sign-in) until the flood stops. Recording a sign-in compares and sets the stored signature counter, so two concurrent uses of one assertion counter cannot both succeed; authenticators that always report a counter of 0 rely on the single-use challenge alone.

</details>

<details>
<summary>Share-link tokens (A2.2)</summary>

Share-link tokens (A2.2) are looked up by public ID and compared by HMAC in constant time; malformed, unknown, wrong and other sites' tokens count against the client IP (30 per 15 minutes) and the site (300 per 15 minutes), and over either limit every token from that IP, or for that site, is refused with `429` until the window ends. IP counts live in a table bounded to 100,000 addresses (IPv6 by /64); once it is full, a failed guess from an address it does not hold is counted in the table's overflow sketch under the same limit (below), so flooding the table neither lifts the limit nor locks out other visitors. Site counts live in a separate table that grows only with the number of sites, so a full IP table never lifts a site's limit. The per-site limit, with the network shares below, means someone guessing from six or more networks can stop new visitors opening links to that one site for as long as they keep going; visitors with a session are unaffected, and the guesses themselves cannot succeed.

</details>

<details>
<summary>API tokens and owner email codes (C1.7)</summary>

API tokens (C1.7) are looked up by public ID and compared by HMAC in constant time; after 30 rejected tokens in 15 minutes an IP is refused every token, valid or not, until the window ends, so guessing cannot continue behind the limit. As for share links, when the owner limiter's table is full a rejected token from an address it does not hold is counted in the table's overflow sketch under the same limit. Owner email-code sends count per address in a table of their own, apart from the per-IP table, so invented addresses cannot crowd out owners signing in from new addresses, and that table overflows into a sketch as the visitor tables do.

</details>

<details>
<summary>Limiter tables under flooding (A2.9)</summary>

Every limiter keyed by something a client chooses (visitor IPs, visitor addresses, per-site networks, owner IPs and addresses, API-token failures) keeps an exact table of 100,000 keys and, once that is full, counts each key it does not hold in an overflow sketch instead of refusing it: a count-min sketch of 2 rows of 2^17 saturating counters for each method (512 KiB each, allocated only on that method's first overflow, so a flood of one method never raises another's counters), indexed by a hash seeded at random per process so a client cannot aim at another's counters, with conservative updates. A key's estimate is never below its true count, so no per-IP, per-address or per-site limit is lifted, and a key that moves from the sketch into the table keeps its count. An unseen client is refused only once both of its counters reach its limit, which takes the flood about N x 2^17 counted attempts of that method in one window from keys the table does not hold: 1.3 million password attempts per 15 minutes, 2.6 million email sends an hour, 3.9 million code entries, OAuth starts or share-link guesses per 15 minutes. Since each client counts only up to its own limit, each of these needs about 131,000 distinct IPv4 addresses or IPv6 /64s in the window: two IPv6 /48s. For owner email codes it is 655,000 sends an hour to invented addresses, which at 20 sends per IP needs about 33,000 IPv4 addresses or /64s each hour, and that fits inside one /48. Below that, filling a table refuses no one it does not hold. The owner limiters' tables (sign-in, and API tokens with CLI login) also hold keys the server owns: invitations and passkey registrations per owner, CLI approvals and denials per owner, and CLI login polls per login. So during a flood those overflow to sketches too, where a collision can only refuse early: an extra `slow_down` for a CLI login poll, or an owner action refused with `429` before the owner reached their own limit. The sketches add at most about 12 MiB over the tables, all in memory and single-instance like the tables themselves. Per-site limits are kept, and a site is shielded from a flood from few networks by network shares: once a site's counter for password attempts, email-code sends, code entries or share-link guesses reaches half its limit in the current window (50 of 100 password attempts, 100 of 200 sends, 150 of 300 entries or guesses), or the site holds half its 1,000 pending OAuth flows, each network (IPv4 /24, IPv6 /48) may spend at most a tenth of the site's limit in that window (10, 20, 30, 30 and 100), counting what it spent before the pressure began; beyond that its attempts answer `429` and spend nothing of the site's limit. Below half, only the per-IP and per-site limits apply, so many visitors behind one office /24 or carrier /48 are not held to a share. Network counts are kept in a bounded table of their own with the same overflow sketch. **Accepted risk:** a per-site limit is a fixed cap, so an attacker with six or more networks (IPv4 /24s or IPv6 /48s) can still keep a sign-in method shut for one site. One network spends the site's limit alone up to the pressure mark, which is not yet limited per network. Five more then spend their tenth each, filling the rest: for example, 50 password attempts from one /24 and 10 from each of five /48s, every 15 minutes. The same goes for 200 sends an hour, 300 entries or guesses per 15 minutes, and 1,000 OAuth flows per 10 minutes, where one network starts 500 and five start 100 each. The method reopens within one window once fewer than six networks keep going. In general the bound is 1 + (1 - 1/sitePressureDivisor) x networkShareDivisor networks; with the pressure mark at half and the share at a tenth, that is 1 + 5 = 6. Ten networks spending only their shares fill a site too. Before A2.9, ten IPv4 addresses or ten IPv6 /64s (part of one /48) were enough. The pressure check and the counts are not one atomic step, so attempts in flight as a site crosses the mark may get slightly more than a network's share (one or two in tests); this is accepted, since the per-site limit itself is never exceeded. Neither flood lets anyone in, and visitors with a session are unaffected.

</details>

### CLI login phishing

The CLI device login (C1.7) lets anyone start a login and send its approval link to an owner; an owner who approves it hands the starter a token. The approval page shows the code to compare with the terminal and warns to approve only a login you started; approving needs a dashboard session from the dashboard origin, never an API token; logins last 15 minutes and are delivered once; and the resulting token is listed, scoped and revocable like any other.

### OAuth-specific risks

Every flow is bound to the browser that started it and to one site, host and policy version, and only verified emails match an allowlist; the auth origin sets no cookie at all.

<details>
<summary>Flow checks: PKCE, nonce, ID tokens and site codes</summary>

PKCE (S256) and `state` on every flow; OIDC `nonce` checked against the flow's, and ID tokens verified for signature, issuer, audience and expiry; allowlists match only verified emails (GitHub organisations count only for GitHub accounts, and still need a verified email as the session's subject); the auth origin hands sites a single-use, 60-second code bound to the site, the host and the policy version rather than a cookie, so a code stolen for one site is useless on another or after use. The auth origin sets no cookie at all.

</details>

<details>
<summary>Login CSRF and session planting (A2.5 decision)</summary>

Without more, an attacker could finish a flow in their own browser, stop before the site callback, and send the victim the `/.wardlock/callback?code=…` link, signing the victim in as the attacker. Starting a flow therefore sets a second host-only cookie on the site, `__Host-wardlock_oauth` (random 256 bits; only its HMAC is kept with the flow; `Secure`, `HttpOnly`, `Path=/`, `SameSite=Lax` so it rides the top-level redirect back from the auth origin, `Max-Age` 600), and a code is redeemed only with the cookie of the browser that started its flow. The callback clears it whether or not it succeeds, and any attempt spends the code. Flows and codes live in memory: a restart drops sign-ins in flight. The number held is bounded in all (50,000) and per site (1,000), with starts limited to 30 per client per 15 minutes, so a flood of starts from many addresses fills only the attacked site's share; expired entries leave in expiry order, so the bound costs no scan. A second sign-in started on the same site in another tab replaces the binding cookie, so the first tab must start again: the price of binding codes to one browser.

</details>

<details>
<summary>Which emails count</summary>

Every address the provider reports as verified may match the allowlist (for GitHub, every verified address on the account, not only the primary one), as SPEC "OAuth and OIDC" says. An owner who lists `example.com` therefore admits any GitHub account that has verified an `@example.com` address. That is what "verified email" means here, and it is the same trust the email-code method places in a mailbox.

</details>

<details>
<summary>Login page form-action</summary>

The login page's CSP keeps `form-action 'self'` unless its policy enables OAuth; then it adds only the origins the buttons' redirect chain passes through (each provider's authorisation endpoint and the auth origin), never a whole scheme, so a password or email form can still post nowhere but the site.

</details>

<details>
<summary>Provider errors in logs</summary>

A failed sign-in is logged with what failed: the provider's error code for a refused code exchange, and only the HTTP status for a failed discovery or userinfo request, never the provider's response body, which is unbounded and may repeat what the visitor sent.

</details>

<details>
<summary>Slow providers</summary>

A provider's code exchange and lookups at the auth origin share one 15-second deadline; GitHub organisation checks run four at a time and stop at the first membership. OIDC discovery runs once, outside any lock, with waiting callers bounded by their own deadlines, and a failed discovery is reused for 10 seconds rather than retried by every visitor.

</details>

<details>
<summary>Return paths</summary>

The provider only ever returns to the auth origin's fixed `/callback/<provider>`, which only ever sends visitors to the site and host the flow started from; the path to return to is checked like the password form's `next` (only a plain path on the site, otherwise `/`).

</details>

### Custom domain takeover

A hostname is only served once DNS proves it: a CNAME to the claiming site's own `<name>.<base domain>`, or a TXT record holding a token unique to that claim. Owners who remove a domain must also delete its DNS record.

<details>
<summary>Re-checks, certificates and dangling records</summary>

A hostname belongs to one site per instance, and the claim conflict error does not reveal who holds it. Re-checks every 24 hours stop serving a hostname whose record has been gone for 72 hours (so between 72 and 96 hours after the record goes), and on-demand TLS only issues for hostnames verified at that moment, so a stray record cannot be used to mint certificates. The remaining risk is a dangling record: if an owner removes a hostname or tears down a site but leaves the CNAME in place, whoever later holds that site name on the instance can claim the hostname. The CLI and dashboard tell owners to delete the record when they remove a domain.

</details>

### Country lookup

The optional country lookup (V5.13) adds one fact to each access-log entry: the country of its stored IP, from a database file on the server. Nothing is sent to a third party: the operator downloads the file, and every lookup reads it locally. It records a country code or `unknown`, never a city or region, and it is off unless the operator turns it on.

<details>
<summary>What it can reveal, and its limits</summary>

- **Less than the IP it comes from.** The country is looked up from the IP as the log stores it, after truncation (IPv4 /24 and IPv6 /48 by default), never the full address, and with no IP stored no country is recorded. Owners already see the truncated IP; the country adds no precision, only a name. Only `country.iso_code` is read from the database, so even a city database yields nothing finer, and analytics counts countries per site and hour without addresses.
- **Who sees it.** Like the rest of the access log, only the site's owner, through the dashboard and API, owner-scoped with the usual same-`404` for another owner's site. For a site with few visitors, a country beside a time and path can still hint at who a visitor was ("the request from Iceland at 3 a.m."); that is as true of the truncated IP.
- **It can be wrong or hidden.** A relay, VPN, Tor exit, corporate proxy, mobile carrier or Apple's iCloud Private Relay shows the country of its exit, not the visitor's; a database can be out of date or simply wrong, and an address it has no country for records `unknown`. Behind a reverse proxy not listed in `WARDLOCK_TRUSTED_PROXIES`, every entry carries the proxy's address and so the proxy's country. Countries are a rough picture of where requests come from, never evidence of where a person is, and never an access control: no policy reads them.
- **The file is trusted input.** The operator chooses it; the server reads at most 512 MiB into memory and checks its structure (the reader's `Verify`) before using it, and a bad file stops lookups with a warning rather than stopping serving. Lookups run in the access-log writer, off the request path, so a slow or broken lookup can delay or drop log entries but never a response. Log lines about the file name its path and build time, never an address.
- **Licences.** DB-IP's Lite database is CC BY 4.0 and its credit is shown wherever countries are; MaxMind's GeoLite2 has its own end-user licence the operator accepts ([DEPLOYMENT](DEPLOYMENT.md#country-lookup)).

</details>

### Supply chain and the server itself

Pinned tool versions (`tools/go.mod`, lockfiles), `govulncheck` in every check run, a minimum release age of one day for npm packages (pnpm's `minimumReleaseAge: 1440` in `pnpm-workspace.yaml`; Go modules have no equivalent, so Go dependencies rely on `go.sum` pinning, review and `govulncheck`), no CGO, and a small dependency set. Releases are built by GoReleaser in CI with checksums (O4.1).

## Public instances and abuse

Wardlock defaults to **invite-only**: there is no open sign-up in v1, the first owner is created by the operator, and further owners are invited by existing owners (or, with `WARDLOCK_INVITES=operator`, only created by the operator). Operators who let others deploy should use a dedicated sites domain, set `WARDLOCK_ABUSE_EMAIL` so visitors can report abuse, and use the operator commands (A2.13) to suspend owners and take sites down while keeping the evidence: [DEPLOYMENT "Operator controls"](DEPLOYMENT.md#operator-controls). The [acceptable use policy](ACCEPTABLE-USE.md) (O4.13) says what owners may not publish; a new owner sees it before their account is created or first used.

<details>
<summary>Abuse risks and what operators should do</summary>

Anyone who runs an instance others can deploy to inherits the usual hosting abuse problems: phishing pages, malware and illegal content served from their domain, and the domain being blocklisted as a result. Operators of such instances should use a dedicated sites domain, monitor new sites (`wardlockd admin list-sites`), keep contact details for every owner, and be ready to act.

Built (A2.13): `wardlockd admin` commands to list owners and sites, suspend and reinstate owners, and take down, restore and tear down sites; an optional abuse report form on the dashboard host (`/report?site=<host>`) that emails the operator; and `WARDLOCK_INVITES=operator` to stop owners inviting others. A takedown or suspension answers a neutral unavailable page on every host of the site and keeps every deploy and file (garbage collection skips them), so evidence survives until the operator decides; a suspended owner's sign-in attempts fail exactly as a wrong code or passkey does, so suspension is not revealed to someone guessing addresses. Reports strip share-link tokens before sending and never record the reporter's IP. Details: SPEC ["Operator controls and abuse reports"](SPEC.md#operator-controls-and-abuse-reports-a213).

The report form is served by Wardlock on the dashboard host, never on the reported site's origin: the site's own scripts and service workers run on its origin, so a form there could be scripted, faked or flooded by the very site being reported. It posts same-origin only, answers like an unknown host for anything but a published site, and counts only valid reports (5 per IP, 20 per network and 200 per instance per hour), with no per-site limit a reported site could spend to silence reporters. The email puts the operator's lines first and quotes every line of the reporter's words with `> `, introduced as their own words with no instruction from Wardlock, so a reporter cannot forge instructions.

The unavailable page sends `Clear-Site-Data: "cache", "storage"`, so a service worker, cached pages or storage the site left in a visitor's browser before the takedown are cleared on the next navigation and cannot keep answering in its place. It deliberately leaves out `"cookies"`: clearing them would make the page differ from what a fresh visitor sees and reveal that the visitor had a session, and a site session cookie is useless while the site is unavailable anyway.

While a site is taken down or its owner suspended, its owner cannot change its custom domains or delete policies (deleting one deletes the sessions it issued, which record who had access), garbage collection keeps its files and access-log retention keeps its entries. Sign-ins and CLI-login approvals check suspension in the same SQL statement that stores them, so nothing created during a race with a suspension survives reinstatement, and `teardown-site` deletes only while the site is still taken down.

Built (O4.13): the [acceptable use policy](ACCEPTABLE-USE.md), a draft awaiting legal review, and the operator's [abuse procedure](DEPLOYMENT.md#handling-abuse-reports) in DEPLOYMENT, with its [preserve-and-report steps](DEPLOYMENT.md#preserving-and-reporting-illegal-material) for illegal material. The dashboard's setup and sign-in-link pages say that continuing means agreeing to the policy and link to it. A sign-in link (from an invitation or `admin create`) is redeemed only when its owner presses Continue on that page, so a mail scanner or link-preview bot that opens the URL cannot use it up; such a fetcher runs no scripts, and even one that did would not press the button.

Built (A2.14): per-owner quotas on bytes stored, sites and deploys per 24 hours, checked in the same transaction as the write so concurrent deploys cannot pass them together, with per-owner overrides by `wardlockd admin quota`; a separate, lower per-file limit for video files (10 MiB by default, or none accepted); and an optional blocklist of file extensions such as installers and executables. Quotas and the blocklist are off by default; DEPLOYMENT ["Deploy storage and limits"](DEPLOYMENT.md#deploy-storage-and-limits) suggests values for an instance others deploy to.

Built (A2.15): optional automated [content screening](#content-screening) with OpenAI's moderation endpoint, which withholds sexual content on every site and emails the operator about anything else it flags.

</details>

### Content screening

Screening is off unless the operator sets `WARDLOCK_MODERATION=openai` and an API key. When it is on, each unique image (as its bytes show, under any name), the text of each page, text file and SVG, and the images they embed leave the server **once**, after the first activation of a deploy that lists them, for **OpenAI's moderation endpoint**, which returns the categories it flags; nothing else is sent, and OpenAI's handling of what it receives is governed by OpenAI's terms. An owner the operator exempts (`wardlockd admin moderation disable --owner`) never has content sent for their own sites, but **bytes identical to a file a non-exempt owner serves are screened as that owner's**, and the verdict then applies to the exempt owner's copy too. A withheld file answers `451` on every host and path that would serve it, and because files are stored by content hash, **withholding applies to those bytes wherever they appear**, on every owner's sites.

<details>
<summary>What leaves the server, to whom, and when</summary>

- **What:** decided from each file's bytes, not its name. Images as base64 data URLs: a PNG, JPEG or WEBP of at most 20 MB as it is; up to 10 frames of a GIF (half evenly spaced, half at random), re-encoded as PNG; a larger PNG or JPEG decoded and scaled down to a JPEG; an icon's largest PNG. The extracted plain text (no markup, scripts, styles or hidden elements, with share-link tokens already redacted; SVG text included) of every file a path serves as displayed text (`.html`, `.htm`, `.shtml`, `.xhtml`, `.xht`, `.xhtm`, `.svg`, `.txt`, `.text`, `.md`, `.markdown`, `.csv`, `.json`, `.xml`) or whose bytes are HTML, XML or SVG, at most 100,000 characters per file, and up to 16 images that any text file, stylesheets and scripts included, embeds as `data:` URIs of any media type whose bytes are an image or SVG (base64, percent-encoded or plain; an embedded SVG's text too). Gzip files are decompressed on the server only to compare them with the file they stand for; nothing of them is sent. Never videos, fonts, or the source of scripts and stylesheets (unless a path serves them as text), file names, paths, site names, owner details, visitor data or access logs. The request carries only the model name, the content and the operator's API key.
- **To whom:** OpenAI, at `https://api.openai.com/v1/moderations`, over HTTPS, model `omni-moderation-latest`. No other provider or third party.
- **When:** in the background after activation (and when a site that was taken down or whose owner was suspended serves again), once per SHA-256 ever: the same bytes under another path, deploy, site or owner are not sent again. An operator's `rescan` sends a file again. Pending deploys and unpublished files are never sent. Content on protected sites is sent like any other: the policy decides who may view a site, not whether the operator screens it.
- **Exemptions:** an exempt owner's sites are not queued, and a queued file is dropped unsent if every owner serving it is exempt when its turn comes. An exemption does not cover bytes a non-exempt owner also serves: those are sent as that owner's, so **an exempt owner's bytes may still be screened** when another, non-exempt owner serves identical bytes, and a quarantine of them withholds the exempt owner's copy too (they are told like any owner). New owners follow the instance default (screened while screening is on).
- **The key** is a `platform.Secret`: it never appears in logs, errors or start-up output, which names only the provider. Errors from the endpoint are reduced to their status code and error code (never its message, which can quote the input), so neither the key nor content can reach a log line through them.
- **Logs and notices** carry hashes, categories, site names and paths, never content. Owners' notices name the file and site, never the categories.

</details>

<details>
<summary>The shared-hash effect, limits of the classifier, and accepted risks</summary>

- **Shared hashes.** Blobs are content-addressed and shared across owners, so the verdict belongs to the bytes, not to a site: if owner A's image is quarantined, owner B's site serving byte-identical bytes answers `451` for it too, and B is told which of their files is withheld. `release` restores it for everyone. This is deliberate: the same bytes are the same content, and per-owner verdicts would let anyone re-publish withheld bytes by uploading them again. It also means one owner's upload can reveal, through the `451`, that another site serves the same bytes; the policy is checked first, so on a protected site only visitors with access ever see it.
- **What the classifier can see.** According to OpenAI's documentation, `sexual/minors` is a text-only category: images score `0` there, so a sexual image of a child is caught only under `sexual` (withheld by default). OpenAI's documentation also asks that known or suspected child sexual abuse material not be sent to the endpoint; screening sends every new image without knowing what it is, so an operator who suspects such material on their instance should follow the [preserve-and-report procedure](DEPLOYMENT.md#preserving-and-reporting-illegal-material) rather than rescan it. Automated classifiers make mistakes both ways; screening supplements abuse reports and the operator's own checks, it does not replace them.
- **Evidence.** A quarantined file's stored copy is kept from garbage collection until the operator discards or releases it, so an owner deleting the deploy or site cannot destroy it before review.
- **Unscreenable images.** An image neither the endpoint nor the standard library can read (AVIF, HEIF, BMP, TIFF, JPEG XL, a bitmap-only icon) or too large to read or scale is served, as the maintainer decided, but flagged `unscreenable` for the operator to review in `moderation list` and the flagged digest. So is a page embedding more than 16 images. SVG drawings are screened only by their text and embedded raster images; the shapes themselves are not rendered or judged.
- **What screening cannot see.** Screening reads stored bytes. An image a page's script assembles at runtime (from `atob`, string concatenation, a canvas, a fetched or decrypted blob) never exists as a stored file or a complete `data:` URI, so it cannot be screened; neither can content loaded from another origin. Animated content is **sampled, not exhaustively checked**: ten GIF frames per screening, half at random, and videos not at all, so a frame shown briefly can be missed. These gaps are why screening supplements abuse reports rather than replacing them.
- **Precompressed siblings.** Screening judges a file's own bytes, so a browser is offered a `.gz` sibling only once the server has decompressed it and found exactly those bytes, and never a `.br` sibling (no brotli decoder in the standard library). Requested by its own path, a sibling is a compressed download like any archive; archives are not unpacked or screened.
- **Caches.** While a file waits for its verdict it is sent `no-cache`, never with long-term caching, so shared caches revalidate it and see the `451` once it is withheld. A browser or cache may still keep a copy fetched before the verdict, and an intermediary that ignores `no-cache` may serve it.
- **Accepted risks.** A file is served until its verdict arrives (seconds after activation, longer while the endpoint rate-limits), and for up to 5 seconds after an operator's decision in another process. Bytes that differ (a re-encoded image) are new content and screened anew. Text past 100,000 characters per file, text in images, animated GIF frames other than the ten sampled (an animation of more than 2,000 frames is unscreenable), images assembled by script at runtime, videos and other file types are not screened.

</details>

## Out of scope for v1

Protection against a malicious operator; hiding the existence of a site (site names travel in DNS lookups and TLS SNI, and custom domains appear in certificate transparency logs; do not put secrets in site names); DRM or copy prevention once a visitor has access; end-to-end encryption of content at rest.

# Security model

Wardlock protects static content with secrets that travel in URLs. That is a deliberate trade-off: a share link must open instantly for people, `curl`, AI fetch tools and link-preview bots that keep no cookies. This document lists what we defend against, how, and what we accept. Implementation rules live in [wardlock-access](../.agents/skills/wardlock-access/SKILL.md); A2.7 checks the implementation against this document.

## Assets and actors

- **Assets:** site content behind non-public policies; share-link tokens, API tokens, passwords and email codes; owner accounts; the server key (`WARDLOCK_SECRET`); the instance's reputation and domain.
- **Actors:** owners; intended visitors; unintended recipients of a forwarded link; automated fetchers (AI tools, preview bots, email scanners); an attacker on the internet; a malicious site owner on a shared instance; anyone running a public instance.

## Threats and mitigations

### Token leakage

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

### The server key

`WARDLOCK_SECRET` keys every stored token, code and session hash and signs site session cookies. There is no key rotation yet: changing it ends every owner (dashboard) session and every site session at once, and, from A2.2 and C1.7, invalidates every share link and API token. Treat it as a long-lived secret and change it only to recover from a leak.

### Cookie tossing and cross-site session abuse

All sites share one parent domain, so a page on `evil.<base>` could set a cookie with `Domain=<base>` that the browser then sends to `docs.<base>`. Wardlock's site cookies use the `__Host-` prefix, which browsers only accept when the cookie is host-only, `Secure` and `Path=/`, so a sibling cannot forge or overwrite them, and Wardlock ignores any session cookie without the prefix. Cookies are signed and bound to one site, so a cookie copied from one site fails on another (and is cleared there). `/.wardlock/logout` only ends a session for a same-origin request, so a sibling site cannot sign a visitor out of another site. Protected responses send `Vary: Cookie` so no shared cache hands one visitor's page to another. The signature also binds the cookie to one policy and its version, so a policy change or deletion ends its sessions on the next request; the cookie's token is stored only as an HMAC, so revocation (logout, and link revocation from A2.2) is checked against the database on every protected request. The site cookie (`__Host-wardlock_site`) and the dashboard cookie (`__Host-wardlock_session`) have different names, MAC purposes and hashes, so neither ever authenticates the other.

Production deployments should serve sites from a **different registrable domain** from the dashboard (for example sites on `example-sites.net`, dashboard on `wardlock.example.com`). Hosted sites run arbitrary JavaScript; keeping them off the dashboard's registrable domain means they are cross-site to it, so `SameSite` protections apply and no site can toss cookies at the dashboard. The single-domain layout is fine for local development and trusted single-owner instances. The [Public Suffix List](https://publicsuffix.org) is an additional option for large public instances.

### XSS from hosted sites

Wardlock serves owner-supplied HTML and JavaScript as is; it does not sanitise sites. Containment comes from isolation, not filtering: every site is its own origin; site cookies are `HttpOnly` and host-only; the dashboard and API live on a different host (ideally a different registrable domain) with a strict Content Security Policy and `X-Frame-Options: DENY`; `/.wardlock/` pages are rendered by Wardlock, not by the site, and send `Cache-Control: no-store`; the login page also sends a `Content-Security-Policy` that allows no scripts and `X-Frame-Options: DENY`. A deploy can never place files under `/.wardlock/`.

### Preview bots and email scanners

Chat apps, email security scanners and link unfurlers fetch URLs automatically. For ordinary links that is intended (AI-readability is a feature). For use-limited links it would spend uses, so those links show an interstitial on GET and count a use only on POST. Bots do not press buttons. The interstitial's form carries a short-lived ticket signed with the server key and bound to the link's current token, not the token itself, so the page never echoes the token and rotating or revoking the link voids any ticket already shown. The button only works from a page on the same host (the logout rule), so a page elsewhere cannot post a ticket for a link of its own and replace a visitor's session with one it chose.

### Path policies and fallbacks

Sites are public until their owner protects them (SPEC "Policies"), so protection is a deliberate choice per site or path rather than a default an owner might assume. A path policy covers URL paths, not just files: `/docs/` also governs `/docs`, so its clean-URL and redirect answers sit behind it. Files that answer for another path (`404.html`, the SPA `index.html`, a clean URL's `page.html`) are checked against their own path's policy too, so a public path under a protected site never serves protected content as a fallback. A request a protected policy refuses gets the same 401 whether or not the path exists, so policies do not reveal a protected site's file names. Matching is case-sensitive, like file paths; `/Docs/` is not covered by a `/docs/` policy, and the owner must protect the paths their deploy actually uses.

### Brute force and enumeration

Tokens carry 256 bits of secret, so guessing is infeasible; even so, failed token checks are rate-limited per IP and per site, as are password attempts and email-code sends and verifications. Responses do not distinguish "no such link" from "wrong token", and the email-code form answers the same for allowed and disallowed addresses. Owner sign-in codes are 6 digits with 5 attempts and a 10-minute lifetime. Owner passkeys (C1.6) are phishing-resistant: the browser binds each assertion to the dashboard origin and relying party, and the server also checks both, the single-use challenge and the signature counter, refusing every failure with one error. A hosted site cannot use an owner's passkey, since its origin is not the dashboard's; with the dashboard on a sibling of the sites (the single-domain setup), the relying party is still the dashboard host itself, never the shared parent domain. Passkey challenges are held in memory, single use, and lost on restart. Sign-in and registration challenges live in separate bounded tables, with per-network (sign-in) and per-owner (registration) caps on open challenges, so a single client or owner cannot fill either; filling the sign-in table still takes about 2,000 IPv4 addresses or IPv6 /48s, which would delay passkey sign-in (never email-code sign-in) until the flood stops. Recording a sign-in compares and sets the stored signature counter, so two concurrent uses of one assertion counter cannot both succeed. Share-link tokens (A2.2) are looked up by public ID and compared by HMAC in constant time; malformed, unknown, wrong and other sites' tokens count against the client IP (30 per 15 minutes) and the site (300 per 15 minutes), and over either limit every token from that IP, or for that site, is refused with `429` until the window ends. IP counts live in a table bounded to 100,000 addresses (IPv6 by /64); once it is full, a failed guess from an address it does not hold is refused with `429` instead of going uncounted, while valid tokens from new addresses keep working, so flooding the table cannot lock out other visitors. Site counts live in a separate table that grows only with the number of sites, so a full IP table never lifts a site's limit. The per-site limit means someone guessing from many addresses can stop new visitors opening links to that one site for up to 15 minutes; visitors with a session are unaffected, and the guesses themselves cannot succeed. API tokens (C1.7) are looked up by public ID and compared by HMAC in constant time; after 30 rejected tokens in 15 minutes an IP is refused every token, valid or not, until the window ends, so guessing cannot continue behind the limit.

### CLI login phishing

The CLI device login (C1.7) lets anyone start a login and send its approval link to an owner; an owner who approves it hands the starter a token. The approval page shows the code to compare with the terminal and warns to approve only a login you started; approving needs a dashboard session from the dashboard origin, never an API token; logins last 15 minutes and are delivered once; and the resulting token is listed, scoped and revocable like any other.

### OAuth-specific risks

PKCE and `state` on every flow; OIDC `nonce` checked; allowlists match only verified emails; the auth origin hands sites a single-use, 60-second, site-bound code rather than a cookie, so a code stolen for one site is useless on another or after use.

### Custom domain takeover

A hostname is only served once DNS proves it: a CNAME to the claiming site's own `<name>.<base domain>`, or a TXT record holding a token unique to that claim. A hostname belongs to one site per instance, and the claim conflict error does not reveal who holds it. Re-checks every 24 hours stop serving a hostname whose record has been gone for 72 hours, and on-demand TLS only issues for hostnames verified at that moment, so a stray record cannot be used to mint certificates. The remaining risk is a dangling record: if an owner removes a hostname or tears down a site but leaves the CNAME in place, whoever later holds that site name on the instance can claim the hostname. The CLI and dashboard tell owners to delete the record when they remove a domain.

### Supply chain and the server itself

Pinned tool versions (`tools/go.mod`, lockfiles), `govulncheck` in every check run, a minimum package release age enforced by pnpm, no CGO, and a small dependency set. Releases are built by GoReleaser in CI with checksums (O4.1).

## Public instances and abuse

Wardlock defaults to **invite-only**: there is no open sign-up in v1, the first owner is created by the operator, and further owners are invited by existing owners. Anyone who runs an instance others can deploy to inherits the usual hosting abuse problems: phishing pages, malware and illegal content served from their domain, and the domain being blocklisted as a result. Operators of such instances should use a dedicated sites domain, monitor new sites, keep contact details for every owner, and be ready to tear sites down. Abuse tooling (reporting, automated scanning, quotas per owner) is out of scope for v1 and listed under "Not planned yet" in the roadmap.

## Out of scope for v1

Protection against a malicious operator; hiding the existence of a site (site names travel in DNS lookups and TLS SNI, and custom domains appear in certificate transparency logs; do not put secrets in site names); DRM or copy prevention once a visitor has access; end-to-end encryption of content at rest.

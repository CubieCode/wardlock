---
name: wardlock-access
description: Security rules for Wardlock's access layer: tokens, share links, cookies, the reserved path, OAuth, rate limits, response headers, access logging and the required test matrix. Load for any change touching internal/edge or internal/access.
---

# Access layer

Read docs/SECURITY-MODEL.md and the access sections of docs/SPEC.md and docs/ARCHITECTURE.md. These rules are not negotiable inside an item; a change to one needs an explicit decision recorded in SECURITY-MODEL.

**Tokens.**
- 256 bits from `crypto/rand`, base64url without padding, prefixed with a short public ID: `wl_<public id>_<secret>`. The public ID names the token in logs, the dashboard and the access log without revealing it.
- Store only `HMAC-SHA-256(server key, token)`. Look up by public ID, compare the MAC with `hmac.Equal` (constant time).
- Never log, echo, return in an error or put a raw token in a URL the server builds (except the share link shown once to its owner).

**Share links.**
- A valid `?t=` token serves the content directly: **200** at the same URL, with `Set-Cookie` in the same response. Never require a redirect; fetchers that keep no cookies must get the page on the first request.
- Only on HTML responses, inject a small inline script that removes `t` with `history.replaceState`. Never alter other content types.
- Check the token on every request that carries it, not just the first; a session cookie created from a link dies when the link is revoked, rotated or expires.
- Also accept `Authorization: Bearer <token>`.
- Enforce site and path scope: a token for site A or `/docs/` grants nothing on site B or `/admin/`.
- One-time (and other use-limited) links show a "Click to view" interstitial on GET and count a use only on the POST, so preview bots and email scanners cannot spend them.

**Cookies.**
- Site session cookies are host-only (no `Domain`), named with the `__Host-` prefix, `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`, and signed or encrypted with the server key. Bind each to its site and policy version.
- The dashboard uses its own `__Host-` cookies; a site cookie never authenticates the dashboard or another site.
- Production docs recommend sites on a different registrable domain from the dashboard: hosted sites run arbitrary JavaScript, and sibling subdomains can toss cookies onto each other's parent domain.
- In local `*.localhost` mode browsers treat `http://` as secure enough for `Secure` and `__Host-`; do not add an insecure fallback.

**Reserved path.** `/.wardlock/` on every site serves login pages (password, email code, OAuth start), the OAuth callback, the use-limited link `view` interstitial, logout, health and, in proxy TLS mode, `tls-ask`. The edge handles it before any deployed file; deploys containing `.wardlock/` paths are rejected at upload and never served.

**OAuth/OIDC.** Providers cannot register wildcard redirect URIs, so OAuth runs on one central auth origin, `auth.<base domain>`. After the provider callback it issues a single-use, short-lived (≤60 s), site-bound code and redirects to `https://<site>/.wardlock/callback`; the edge exchanges the code server-side for a host-only cookie. Use PKCE (S256) and `state` on every flow; validate `nonce` for OIDC. Allowlists match verified emails only.

**Rate limits** (in-memory limiters per IP, per site, per network under pressure and, for email codes, per address): password attempts, email-code sends, email-code verification, OAuth starts and token guesses (unknown public IDs and bad MACs). Responses must not reveal which part was wrong.

**Response headers on protected sites:** `X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer` (or `same-origin`), `Cross-Origin-Resource-Policy: same-origin`, `Cache-Control: private` (`no-store` on login, interstitial and callback pages, server errors and any response that sets or clears a cookie). Never block AI fetchers in `robots.txt`; access is the policy's job.

**Access log.** One entry per access: token public ID (or policy kind), site, path, time, IP (truncated per config) and User-Agent. Never raw tokens. Writes are buffered and batched off the request path; serving never waits on the database.

**Required test matrix** for any change here (table-driven, through the real edge handler):
- valid, expired, revoked, rotated (old token fails, new works), wrong-site and wrong-path tokens;
- no-cookie fetchers: the first request with `?t=` and with `Authorization: Bearer` returns 200 with content and no redirect;
- `?format=md|txt` with a valid token works without cookies;
- the `t` parameter is stripped only in HTML responses;
- one-time links: GET does not consume; POST does; a second POST fails;
- cookie isolation: a cookie from site A grants nothing on site B or on the dashboard;
- a deploy cannot override `/.wardlock/`;
- rate limits trigger and reset;
- logs and access log contain public IDs, never raw tokens (assert on captured log output).

Fuzz token parsing and cookie decoding (`go test -fuzz`) when you change them.

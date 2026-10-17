# Deployment

Wardlock is pre-alpha. This document describes how a v1 instance is meant to run so that roadmap items build towards it; the parts that need unbuilt features are marked with their roadmap ID. Nothing here should be used in production yet.

Wardlock needs one machine, one binary and a data directory. SQLite is embedded; there is no database server, cache or queue to run. Docker is optional.

## Requirements

- Linux (amd64 or arm64) or macOS; Windows is untested.
- A DNS zone you control, with a wildcard record for sites.
- Ports 80 and 443 reachable from the internet for automatic TLS, or a reverse proxy in front.
- An SMTP server for email codes (A2.4) and owner sign-in codes (C1.1).
- Persistent storage for `WARDLOCK_DATA_DIR`, on a local filesystem (not NFS, because of SQLite locking).
- A server key in `WARDLOCK_SECRET`: at least 32 random bytes, base64url (`openssl rand -base64 32 | tr '+/' '-_'`). `wardlockd serve` and `wardlockd admin` refuse to start without it. See [Backups](#backups).

## Domains and DNS

Pick a base domain for sites and a host for the dashboard. Prefer a separate registrable domain for sites ([why](SECURITY-MODEL.md#cookie-tossing-and-cross-site-session-abuse)):

| Record             | Example                  | Points to               |
| ------------------ | ------------------------ | ----------------------- |
| Wildcard for sites | `*.example-sites.net`    | the server (A/AAAA)     |
| Auth origin (A2.5) | `auth.example-sites.net` | covered by the wildcard |
| Dashboard          | `wardlock.example.com`   | the server              |

```sh
WARDLOCK_BASE_DOMAIN=example-sites.net
WARDLOCK_DASHBOARD_HOST=wardlock.example.com
```

Wildcard certificates need the DNS-01 challenge, so automatic TLS needs an API token for the DNS provider of the sites zone (Cloudflare or deSEC; see [TLS](#tls)). Custom domains for individual sites use on-demand TLS with HTTP-01/TLS-ALPN-01 after the domain is verified.

### Custom domains (C1.9)

Owners add their own hostnames to a site; the operator sets nothing up per domain. The owner creates a `CNAME` from their hostname to `<name>.<base domain>`, which reaches the server through the sites wildcard. Apex domains and CDN-proxied records use the TXT fallback described in [SPEC](SPEC.md#custom-domains-c19-d35) plus A/AAAA records (or ALIAS/flattening) pointing at the server, so publish the server's addresses to owners who need them. Ports 80 and 443 must be reachable from the internet for HTTP-01/TLS-ALPN-01.

`WARDLOCK_DNS_RESOLVERS` (comma-separated `host:port`, default the system resolver) chooses where verification lookups go. Use public or authoritative-facing resolvers if the local one caches aggressively or answers split-horizon records.

## TLS

`WARDLOCK_TLS_MODE` chooses who terminates TLS:

| Mode    | Default when                                     | Behaviour                                                                                                                                                           |
| ------- | ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `local` | base domain and dashboard host are `*.localhost` | Plain HTTP on `WARDLOCK_HTTP_ADDR`. Browsers treat `http://*.localhost` as a secure context. Refused for any other host.                                            |
| `proxy` | any other host                                   | Plain HTTP on `WARDLOCK_HTTP_ADDR` behind a TLS-terminating proxy ([Behind Caddy or nginx](#behind-caddy-or-nginx)). URLs Wardlock prints use `https://`.           |
| `auto`  | never: set it explicitly                         | Wardlock obtains and renews its own certificates with ACME (certmagic) and serves HTTPS on `WARDLOCK_HTTPS_ADDR`; plain HTTP answers ACME challenges and redirects. |

A misconfiguration stops `wardlockd serve` with a message naming the variable: an unknown mode, `local` with public hosts, `auto` with `localhost` or IP hosts or without a DNS provider and token, or an `auto`-only variable (`WARDLOCK_ACME_*`, `WARDLOCK_DNS_*`) or `WARDLOCK_TRUSTED_PROXIES` set in a mode that ignores it.

### Automatic TLS

```sh
WARDLOCK_TLS_MODE=auto
WARDLOCK_BASE_DOMAIN=example-sites.net
WARDLOCK_DASHBOARD_HOST=wardlock.example.com
WARDLOCK_DNS_PROVIDER=cloudflare          # or desec
WARDLOCK_DNS_API_TOKEN=…                  # keep it in the env file, mode 0640
WARDLOCK_ACME_EMAIL=ops@example.com       # optional: expiry and account notices
```

| Variable                 | Default                                          | Meaning                                                                                      |
| ------------------------ | ------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| `WARDLOCK_HTTP_ADDR`     | `:80` in auto mode                               | ACME HTTP-01 answers, `/healthz`, and a redirect to HTTPS for everything else                |
| `WARDLOCK_HTTPS_ADDR`    | `:443`                                           | Sites, the dashboard and the API over TLS (HTTP/2 and HTTP/1.1, TLS 1.2 or later)            |
| `WARDLOCK_ACME_CA`       | `https://acme-v02.api.letsencrypt.org/directory` | ACME directory. Use `https://acme-staging-v02.api.letsencrypt.org/directory` for a first try |
| `WARDLOCK_ACME_EMAIL`    | none                                             | ACME account contact                                                                         |
| `WARDLOCK_ACME_CA_ROOT`  | system roots                                     | PEM bundle to trust for the ACME server itself (a private CA or Pebble)                      |
| `WARDLOCK_DNS_PROVIDER`  | required                                         | `cloudflare` or `desec`                                                                      |
| `WARDLOCK_DNS_API_TOKEN` | required                                         | The provider's API token; never logged                                                       |

Setting `auto` agrees to the CA's terms of service. What Wardlock certifies:

- `*.<base domain>` through DNS-01. One wildcard covers every site and the auth origin `auth.<base domain>` (A2.5).
- The dashboard host: through DNS-01 with the same token when it is the base domain itself or deeper inside its zone (one label under it is already covered by the wildcard), otherwise through HTTP-01 or TLS-ALPN-01, so the token needs no access to the dashboard's zone.
- Custom domains (C1.9) on demand, at the first TLS handshake for the name, through HTTP-01 or TLS-ALPN-01, and only while the domain is verified and active. A name that has never been active (unverified or pending domains, nested names under the base domain, IP addresses) gets no certificate and the handshake fails. A domain that lapses or is removed after its certificate was issued may still complete the handshake with that cached certificate until it is due for renewal or the server restarts, but the edge answers it with the same 404 as an unknown host, and renewal is refused because the decision no longer allows it. C1.9 removes the certificate when a domain is deactivated.

ACME validation reaches the server on ports 80 and 443 from the internet, and the redirect and every URL Wardlock prints use the default HTTPS port, so publish ports 80 and 443. The listen addresses may differ when something forwards them, for example in Docker (`WARDLOCK_HTTP_ADDR=:8080`, `WARDLOCK_HTTPS_ADDR=:8443`, published as `80:8080` and `443:8443`); the systemd unit can bind 80 and 443 directly. Both listeners drop idle keep-alive connections after 120 seconds and slow request headers after 10 seconds; the plain-HTTP listener also limits whole requests and responses to 30 seconds. The HTTPS listener has no whole-request limit, so slow uploads and downloads of large deploy files still complete. Certificates obtain and renew in the background, retrying with backoff; failures are logged with the name and the CA's reason. Until the first wildcard arrives, handshakes for site hosts fail.

Certificates, keys and the ACME account live in `<WARDLOCK_DATA_DIR>/certs` (certmagic file storage). They are re-issued if lost, but keep them across restarts to stay within the CA's rate limits.

**DNS tokens.** Give the token the narrowest scope the provider allows and nothing else:

- **Cloudflare:** an API token (not the global API key) with the permissions _Zone → DNS → Edit_ and _Zone → Zone → Read_, with _Zone Resources_ set to the sites zone only. Optionally restrict _Client IP Address Filtering_ to the server.
- **deSEC:** a token without permission to manage tokens. Restrict it with token policies: a default policy that denies writes, and one policy allowing writes to `TXT` records at subname `_acme-challenge` in the base domain (and at `_acme-challenge.<dashboard label>` if the dashboard host is deeper in that zone). Set _allowed subnets_ to the server's address.

DNS-01 records are created under `_acme-challenge.<base domain>` and removed after validation.

**Testing issuance.** `internal/tls` tests run the whole flow against Pebble, Let's Encrypt's test CA, in process with a fake DNS server and DNS provider, as part of `pnpm check`; no Docker, network or credentials are involved. To try a real server without touching Let's Encrypt, run [Pebble](https://github.com/letsencrypt/pebble) with `pebble-challtestsrv` as its DNS server, point `WARDLOCK_ACME_CA` at Pebble's directory and `WARDLOCK_ACME_CA_ROOT` at its `test/certs/pebble.minica.pem`, and resolve your test names to the server through the challenge test server. A real DNS provider is needed for DNS-01 outside the tests, so use the Let's Encrypt staging directory with a throwaway zone instead when you want to check provider credentials.

## Install a release binary

Releases are not published yet (O4.4). When they are, each GitHub release carries `wardlockd_<version>_<os>_<arch>` and `wardlock_<version>_<os>_<arch>` archives for linux, darwin and windows on amd64 and arm64 (`.zip` on Windows), `checksums.txt` (SHA-256 of every archive and SBOM) and an SPDX SBOM per archive (`<archive>.sbom.json`). The wardlockd archive also contains `wardlockd.service`.

[`deploy/install.sh`](../deploy/install.sh) installs the binary for this machine (Linux or macOS) after checking the archive against `checksums.txt`; it refuses a missing or mismatched checksum and never runs `sudo` itself:

```sh
sh install.sh --version 0.1.0 --component wardlockd   # or wardlock (default), or all
sh install.sh --component all --dir ~/.local/bin      # latest release, no root
```

`--base-url` installs from a mirror or a local snapshot directory instead, for example `--base-url file://$PWD/.local/goreleaser --version 0.0.1-snapshot.abc1234`.

## Single binary with systemd

```sh
sudo useradd --system --home /var/lib/wardlock --create-home wardlock
sudo sh deploy/install.sh --version 0.1.0 --component wardlockd
sudo install -m 0640 -o root -g wardlock wardlock.env /etc/wardlock/wardlock.env
sudo install -m 0644 deploy/wardlockd.service /etc/systemd/system/wardlockd.service
sudo systemctl daemon-reload && sudo systemctl enable --now wardlockd
```

[`deploy/wardlockd.service`](../deploy/wardlockd.service) runs as the `wardlock` user with `StateDirectory=wardlock` (so `WARDLOCK_DATA_DIR=/var/lib/wardlock`), `AmbientCapabilities=CAP_NET_BIND_SERVICE` for ports 80/443, and systemd hardening (`ProtectSystem=strict`, `NoNewPrivileges`, private `/tmp`, a `@system-service` system call filter, `UMask=0077`).

On first start with no owners, the log shows a one-time setup URL (`setup_url`, valid 60 minutes, single use, a new one on every start until an owner exists): `journalctl -u wardlockd | grep setup_url`. Or run `sudo -u wardlock wardlockd admin create --email you@example.com` with the same environment as the service; it creates the owner and prints a one-time sign-in URL valid for 60 minutes. Afterwards owners sign in with an emailed code and invite further owners from the dashboard; invitation links are valid for 7 days. Setup, sign-in and invitation URLs use `https://<dashboard host>` with no port, since production dashboards are reached over HTTPS on the default port; only a `localhost` or `*.localhost` dashboard host gets `http://` and the listen port.

The per-address limit on sign-in emails (5 per hour) counts every request for that address, whoever sends it. Anyone who knows an owner's address can spend those sends, which delays that owner's sign-in by up to an hour; it cannot sign them in. Passkeys (C1.6) avoid this.

## Docker

[`deploy/Dockerfile`](../deploy/Dockerfile) packages the static `wardlockd` into a distroless image that runs as the `nonroot` user (uid 65532) with data in the `/data` volume. Release images will be multi-arch (`linux/amd64`, `linux/arm64`) at `ghcr.io/cubiecode/wardlock:<version>` and `:latest`; none are published yet (O4.4). [`deploy/compose.yaml`](../deploy/compose.yaml) is an example with a read-only root filesystem, all capabilities dropped and one volume for data. Until images are published, build a snapshot and point the example at it:

```sh
goreleaser release --snapshot --clean   # images ghcr.io/cubiecode/wardlock:<version>-amd64 and -arm64
export WARDLOCK_SECRET=$(openssl rand -base64 32 | tr '+/' '-_')   # keep it; see Backups
WARDLOCK_IMAGE=ghcr.io/cubiecode/wardlock:<version>-amd64 docker compose -f deploy/compose.yaml up -d
curl http://127.0.0.1:8080/healthz
```

## Behind Caddy or nginx

If something else already terminates TLS on the machine, run Wardlock in proxy mode (`WARDLOCK_TLS_MODE=proxy`, the default for non-`localhost` hosts) on a loopback port and proxy every site host and the dashboard to it, preserving the `Host` header. Examples: [`deploy/Caddyfile.example`](../deploy/Caddyfile.example), [`deploy/nginx.conf.example`](../deploy/nginx.conf.example). Then:

- Pass `Host` unchanged and set `X-Forwarded-For` and `X-Forwarded-Proto`. Set `WARDLOCK_TRUSTED_PROXIES` to the proxy's addresses, comma-separated IP addresses or CIDR ranges (`127.0.0.1,::1` for a proxy on the same machine). A range covering every address (`0.0.0.0/0`, `::/0`) is refused, and IPv4-mapped forms (`::ffff:10.0.0.1`) are read as IPv4. Wardlock believes forwarding headers only from those peers: it reads `X-Forwarded-For` from the right, skipping trusted hops, so the first untrusted address is the client and anything a client prepends is ignored; it keeps `X-Forwarded-Proto` only when it is `http` or `https`; and it removes `X-Forwarded-*`, `X-Real-IP` and `Forwarded` from every other peer. `X-Forwarded-Host` is never used: routing is by `Host`.
- Without `WARDLOCK_TRUSTED_PROXIES` (Wardlock logs a warning at startup), Wardlock sees every client behind the proxy as the proxy's address, so the per-IP sign-in limits (email-code sends and verifications, setup and sign-in-link redemptions) become instance-wide: one busy or hostile client can make sign-in wait for everyone until the window resets. Never list an address that untrusted clients can connect from, or they can choose their own address.
- Do not cache protected responses at the proxy, and do not log query strings (they carry share-link tokens), or redact the `t` parameter. The examples do this.
- Allow request bodies of at least the per-file limit (25 MiB default) for blob uploads.
- For custom domains, the proxy must obtain their certificates. With Caddy, use on-demand TLS with `ask` set to `http://127.0.0.1:8080/.wardlock/tls-ask`, so it only requests certificates for hostnames Wardlock has verified (C1.9 adds this to the example). nginx cannot issue certificates on demand; use Caddy, or manage custom-domain certificates yourself.

## SMTP

`WARDLOCK_SMTP_URL` (F0.4) chooses how mail leaves the server:

- `smtp://user:pass@host:587` connects in plain text and upgrades with STARTTLS before authenticating. A server that does not offer STARTTLS is refused; there is no option to send in plain text. The port defaults to 587.
- `smtps://user:pass@host:465` uses TLS from the first byte. The port defaults to 465.
- `dev://` (the default) delivers nothing. It writes each message to `.local/mail/` (relative to the working directory) as a readable `.eml` file and logs only the recipient, subject and file name, never the body or code. Use it for development only.

The server certificate is always verified against the system roots. Percent-encode special characters in the user name and password (`@` is `%40`). Credentials never appear in logs or errors, which show the server as `smtp://[redacted]@host:port`. `WARDLOCK_SMTP_FROM` sets the sender (`Wardlock <no-reply@example.com>`); it defaults to `Wardlock <wardlock@<dashboard host>>`. Configure SPF, DKIM and DMARC for the sending domain or codes will land in spam.

## Deploy storage and limits

Deploy file bodies are stored once per content hash under `WARDLOCK_BLOB_DIR` (default `<WARDLOCK_DATA_DIR>/blobs`, laid out as `sha256/ab/cdef…`). Each upload is written to a temporary file in the same directory, fsynced and renamed into place, so put the blob directory on the same local filesystem as its parent, and give it room for every deploy you keep. Unreferenced blobs are removed by garbage collection (see below).

| Variable                    | Default              | Limit                   |
| --------------------------- | -------------------- | ----------------------- |
| `WARDLOCK_MAX_FILE_SIZE`    | `26214400` (25 MiB)  | bytes per file          |
| `WARDLOCK_MAX_DEPLOY_FILES` | `20000`              | files per deploy        |
| `WARDLOCK_MAX_DEPLOY_SIZE`  | `1073741824` (1 GiB) | bytes in a whole deploy |

Values are whole numbers of at least 1, without units. A deploy over a limit is rejected when it is created (`413 limit_exceeded`, naming the file where one is at fault), and an upload body longer than the per-file limit is rejected too. Raising the per-file limit means raising a reverse proxy's request body limit to match.

### Garbage collection

The server prunes deploy history and removes file bodies no deploy references at start and then every `WARDLOCK_GC_INTERVAL_MINUTES`. Each site keeps its newest `WARDLOCK_DEPLOY_HISTORY` deploys plus its active deploy, which is never pruned, so rollback works within that history. Deploys not yet activated and bodies uploaded within `WARDLOCK_GC_GRACE_HOURS` are kept, so a slow upload is never cut short; keep the grace period longer than your longest deploy. `wardlockd gc` runs one collection with the same settings and prints what it removed. It is safe to run while the server is running, for example from cron with `WARDLOCK_GC_INTERVAL_MINUTES=0`.

| Variable                       | Default | Meaning                                                               |
| ------------------------------ | ------- | --------------------------------------------------------------------- |
| `WARDLOCK_GC_INTERVAL_MINUTES` | `60`    | minutes between collections in the server, 0 to 525600; 0 runs none   |
| `WARDLOCK_GC_GRACE_HOURS`      | `24`    | hours that new uploads and pending deploys are protected, 1 to 8760   |
| `WARDLOCK_DEPLOY_HISTORY`      | `50`    | newest deploys kept per site, 1 to 100000; the active one is kept too |

Every run logs `garbage collection finished` with `deploys_pruned`, `blobs_deleted`, `files_deleted` (stored files with no database record, left by a crash mid-upload, and abandoned temporary uploads) and `bytes_freed`, or `garbage collection failed` with the error; a failed run is retried at the next interval.

## Access log

Every request for a published site's content (A2.8) adds one entry to the access log in the database: time, site, host, path, status, how the visitor got in (`public`, `link`, `password`, `email`, `oauth`, or `none` when refused), the share link's public ID, a truncated client IP and the User-Agent. Health checks, `/.wardlock/` pages, the dashboard and hosts with nothing published are not logged.

| Variable                             | Default | Meaning                                                          |
| ------------------------------------ | ------- | ---------------------------------------------------------------- |
| `WARDLOCK_ACCESS_LOG_IPV4_PREFIX`    | `24`    | leading bits of an IPv4 address kept, 0 to 32; 0 stores no IP    |
| `WARDLOCK_ACCESS_LOG_IPV6_PREFIX`    | `48`    | leading bits of an IPv6 address kept, 0 to 128; 0 stores no IP   |
| `WARDLOCK_ACCESS_LOG_RETENTION_DAYS` | `90`    | days to keep entries, 0 to 36500; 0 keeps them forever           |
| `WARDLOCK_ACCESS_LOG_BUFFER`         | `10000` | entries that may wait for a write, 1 to 1000000, before dropping |

The defaults keep enough of an address to tell networks apart, not individual households; set both prefixes to their maximum to keep full addresses, or to 0 to store none. IPv4-mapped IPv6 addresses count as IPv4. The IP is the client's address after trusted-proxy resolution: behind a reverse proxy listed in `WARDLOCK_TRUSTED_PROXIES` it is the client from `X-Forwarded-For`; without that setting every entry carries the proxy's address. Paths are stored without their query string, and any token in the path or User-Agent keeps only its public ID, so the access log never holds a share link.

Entries are written in batches off the request path: serving never waits on the database. If the database falls behind and the buffer fills, new entries are dropped rather than slowing visitors; the server logs a warning (`access log entries dropped`, at most every 10 seconds) with the running total, and a failed batch write is logged and counted the same way. On shutdown the server writes what is buffered within the shutdown timeout. Retention runs at start and hourly beside the writer, deleting entries older than the retention period (including those of deleted sites) 1,000 at a time so the database is never locked for long.

## Backups

Everything lives in `WARDLOCK_DATA_DIR`: `wardlock.db` and `blobs/` (or `WARDLOCK_BLOB_DIR` if set; and `certs/`, which can be re-issued).

- **Database:** never copy `wardlock.db` while the server runs. Use the online backup command (O4.2), `wardlockd backup /path/to/backup.db`, which uses SQLite's backup API to write a consistent copy, or stop the service first. As an option, [Litestream](https://litestream.io) can replicate the database continuously to object storage.
- **Blobs:** content-addressed and write-once, so a plain `rsync` (after the database backup) is consistent: every blob the backed-up database references already exists, provided garbage collection does not run in between. Schedule backups away from collections, or set `WARDLOCK_GC_INTERVAL_MINUTES=0` and run `wardlockd gc` after each backup.
- **Restore** (O4.2): stop the server, put the database and `blobs/` back in an empty data directory, start the server. Migrations run automatically; a server older than the database refuses to start.

Keep `WARDLOCK_SECRET` with your backups, separately and securely. Without it, every share link, API token and session stops working after a restore.

## Upgrades

Replace the binary or image and restart. Migrations are forward-only and run on start. Back up first; downgrading after a migration needs the backup.

## Go import paths

The Go modules use vanity import paths on the project's own domain: `wardlock.nz/server`, `wardlock.nz/cli` and `wardlock.nz/apiclient`. This lets the repository move without breaking imports. For `go get` / `go install` to work, `https://wardlock.nz/<module>?go-get=1` must return HTML with a `go-import` meta tag mapping the module path to the repository and its subdirectory:

```html
<!-- https://wardlock.nz/cli?go-get=1 -->
<meta
  name="go-import"
  content="wardlock.nz/cli git https://github.com/CubieCode/wardlock apps/cli"
/>
<!-- https://wardlock.nz/server?go-get=1 -->
<meta
  name="go-import"
  content="wardlock.nz/server git https://github.com/CubieCode/wardlock apps/server"
/>
<!-- https://wardlock.nz/apiclient?go-get=1 -->
<meta
  name="go-import"
  content="wardlock.nz/apiclient git https://github.com/CubieCode/wardlock packages/apiclient-go"
/>
```

The fourth field is the module's directory inside the repository; Go supports it from 1.25. Until the domain serves this metadata, local builds rely on `go.work` and the `replace` directives in `apps/server/go.mod` and `apps/cli/go.mod`. Serving it is a launch task (O4.4); no DNS or hosting change is part of the current work.

## Releases and module tags

A release has one version, `vX.Y.Z`, and four tags on the same release commit:

| Tag                            | Used by                                              |
| ------------------------------ | ---------------------------------------------------- |
| `vX.Y.Z`                       | GoReleaser (archives, image, GitHub release)         |
| `packages/apiclient-go/vX.Y.Z` | `go get wardlock.nz/apiclient@vX.Y.Z`                |
| `apps/cli/vX.Y.Z`              | `go install wardlock.nz/cli/cmd/wardlock@vX.Y.Z`     |
| `apps/server/vX.Y.Z`           | `go install wardlock.nz/server/cmd/wardlockd@vX.Y.Z` |

Module tags are prefixed with the module's directory, the fourth `go-import` field; a plain `vX.Y.Z` tag is not a version of any module. `go install pkg@version` refuses a module whose `go.mod` has `replace` directives, so the tagged commit cannot be the development state. The release steps are:

1. On the release branch, tag `packages/apiclient-go/vX.Y.Z` at the commit to release.
2. In `apps/cli/go.mod` and `apps/server/go.mod`, drop the `replace wardlock.nz/apiclient` directives, require `wardlock.nz/apiclient vX.Y.Z` where it is used, and run `GOWORK=off go mod tidy` so `go.sum` records the client's hash (the release owner holds the lockfile gate for this). Commit.
3. Tag that commit `vX.Y.Z`, `apps/cli/vX.Y.Z` and `apps/server/vX.Y.Z`, push the tags, and run the release workflow's publish job. Main keeps its `replace` directives; only the release commit drops them.

[`scripts/test-module-tags.sh`](../scripts/test-module-tags.sh) proves this scheme without publishing: it makes the release commit and tags in a temporary clone, serves that clone with `git daemon` and answers `wardlock.nz/<module>?go-get=1` from a local proxy, then runs `go install` for both commands. It also checks that an unprefixed tag and a tag on a commit with `replace` directives fail. The tags exist only in the deleted clone. The release workflow runs it on every pull request.

[`.goreleaser.yaml`](../.goreleaser.yaml) builds every artefact, and [`.github/workflows/release.yml`](../.github/workflows/release.yml) runs `goreleaser release --snapshot --clean` on pull requests and main, then [`scripts/release-smoke.sh`](../scripts/release-smoke.sh) checks the snapshot: all 12 archives and SBOMs match `checksums.txt`, `install.sh` installs them and rejects a bad checksum, and the image runs as uid 65532 and answers `/healthz`. Publishing is off three ways: snapshots never push, the image and GitHub release are skipped outside snapshots unless `WARDLOCK_PUBLISH=true`, and the publish job that sets it is `if: false`. Turning it on is O4.4 and needs explicit authorisation.

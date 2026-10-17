# Deployment

Wardlock is pre-alpha. This document describes how a v1 instance is meant to run so that roadmap items build towards it; the parts that need unbuilt features are marked with their roadmap ID. Nothing here should be used in production yet.

Wardlock needs one machine, one binary and a data directory. SQLite is embedded; there is no database server, cache or queue to run. Docker is optional.

## Requirements

- Linux (amd64 or arm64) or macOS; Windows is untested.
- A DNS zone you control, with a wildcard record for sites.
- Ports 80 and 443 reachable from the internet for automatic TLS, or a reverse proxy in front.
- An SMTP server for email codes (A2.4) and owner sign-in codes (C1.1).
- Persistent storage for `WARDLOCK_DATA_DIR`, on a local filesystem (not NFS, because of SQLite locking).

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

Wildcard certificates need the DNS-01 challenge, so Wardlock needs an API token for your DNS provider (C1.4; Cloudflare first, then one other `libdns` provider). Give the token the narrowest scope the provider allows (edit DNS records for this zone only). Custom domains for individual sites use on-demand TLS with HTTP-01/TLS-ALPN-01 after the domain is verified.

## Single binary with systemd

```sh
sudo useradd --system --home /var/lib/wardlock --create-home wardlock
sudo install -m 0755 wardlockd /usr/local/bin/wardlockd
sudo install -m 0640 -o root -g wardlock wardlock.env /etc/wardlock/wardlock.env
sudo install -m 0644 deploy/wardlockd.service /etc/systemd/system/wardlockd.service
sudo systemctl daemon-reload && sudo systemctl enable --now wardlockd
```

[`deploy/wardlockd.service`](../deploy/wardlockd.service) runs as the `wardlock` user with `StateDirectory=wardlock` (so `WARDLOCK_DATA_DIR=/var/lib/wardlock`), `AmbientCapabilities=CAP_NET_BIND_SERVICE` for ports 80/443, and systemd hardening (`ProtectSystem=strict`, `NoNewPrivileges`, private `/tmp`). Release binaries and an install script are O4.1.

On first start with no owners, the log shows a one-time setup URL (C1.1): `journalctl -u wardlockd | grep setup`. Or run `sudo -u wardlock wardlockd admin create --email you@example.com`.

## Docker

[`deploy/Dockerfile`](../deploy/Dockerfile) builds a static image (distroless, non-root); [`deploy/compose.yaml`](../deploy/compose.yaml) is an example with one volume for data. Release images will be published to `ghcr.io/cubiecode/wardlock` (O4.1); none are published yet.

```sh
docker compose -f deploy/compose.yaml up -d
```

## Behind Caddy or nginx

If something else already terminates TLS on the machine, run Wardlock in plain-HTTP mode (C1.4) on a loopback port and proxy every site host and the dashboard to it, preserving the `Host` header. Examples: [`deploy/Caddyfile.example`](../deploy/Caddyfile.example), [`deploy/nginx.conf.example`](../deploy/nginx.conf.example). Then:

- Pass `Host` unchanged and set `X-Forwarded-For` and `X-Forwarded-Proto`; Wardlock trusts these only from addresses in its trusted-proxy setting (C1.4).
- Do not cache protected responses at the proxy, and do not log query strings (they carry share-link tokens), or redact the `t` parameter. The examples do this.
- Allow request bodies of at least the per-file limit (25 MiB default) for blob uploads.

## SMTP

`WARDLOCK_SMTP_URL` (F0.4): `smtp://user:pass@host:587` (STARTTLS required), `smtps://user:pass@host:465`, or `dev://` for development, which logs each message to the console and writes it to `.local/mail/`. Configure SPF, DKIM and DMARC for the sending domain or codes will land in spam.

## Backups

Everything lives in `WARDLOCK_DATA_DIR`: `wardlock.db` and `blobs/` (and `certs/`, which can be re-issued).

- **Database:** never copy `wardlock.db` while the server runs. Use the online backup command (O4.2), `wardlockd backup /path/to/backup.db`, which uses SQLite's backup API to write a consistent copy, or stop the service first. As an option, [Litestream](https://litestream.io) can replicate the database continuously to object storage.
- **Blobs:** content-addressed and write-once, so a plain `rsync` (after the database backup) is consistent: every blob the backed-up database references already exists.
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

The fourth field is the module's directory inside the repository; Go supports it from 1.25. Release tags for such modules are prefixed with that directory (for example `apps/cli/v0.1.0`). O4.1 must confirm the exact tag scheme with `go install wardlock.nz/cli/cmd/wardlock@<version>` against a test tag before the first release. Until the domain serves these tags, local builds rely on `go.work` and the `replace` directives in `apps/server/go.mod` and `apps/cli/go.mod`. Serving the tags is a launch task (O4.4); no DNS or hosting change is part of the current work.

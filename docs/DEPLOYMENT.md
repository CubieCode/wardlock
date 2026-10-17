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

Wildcard certificates need the DNS-01 challenge, so Wardlock needs an API token for your DNS provider (C1.4; Cloudflare first, then one other `libdns` provider). Give the token the narrowest scope the provider allows (edit DNS records for this zone only). Custom domains for individual sites use on-demand TLS with HTTP-01/TLS-ALPN-01 after the domain is verified.

### Custom domains (C1.9)

Owners add their own hostnames to a site; the operator sets nothing up per domain. The owner creates a `CNAME` from their hostname to `<name>.<base domain>`, which reaches the server through the sites wildcard. Apex domains and CDN-proxied records use the TXT fallback described in [SPEC](SPEC.md#custom-domains-c19-d35) plus A/AAAA records (or ALIAS/flattening) pointing at the server, so publish the server's addresses to owners who need them. Ports 80 and 443 must be reachable from the internet for HTTP-01/TLS-ALPN-01.

`WARDLOCK_DNS_RESOLVERS` (comma-separated `host:port`, default the system resolver) chooses where verification lookups go. Use public or authoritative-facing resolvers if the local one caches aggressively or answers split-horizon records.

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

If something else already terminates TLS on the machine, run Wardlock in plain-HTTP mode (C1.4) on a loopback port and proxy every site host and the dashboard to it, preserving the `Host` header. Examples: [`deploy/Caddyfile.example`](../deploy/Caddyfile.example), [`deploy/nginx.conf.example`](../deploy/nginx.conf.example). Then:

- Pass `Host` unchanged and set `X-Forwarded-For` and `X-Forwarded-Proto`; Wardlock trusts these only from addresses in its trusted-proxy setting (C1.4).
- Until trusted-proxy support lands (C1.4), Wardlock sees every client behind the proxy as the proxy's address, so the per-IP sign-in limits (email-code sends and verifications, setup and sign-in-link redemptions) become instance-wide: one busy or hostile client can make sign-in wait for everyone until the window resets.
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

Deploy file bodies are stored once per content hash under `WARDLOCK_BLOB_DIR` (default `<WARDLOCK_DATA_DIR>/blobs`, laid out as `sha256/ab/cdef…`). Each upload is written to a temporary file in the same directory, fsynced and renamed into place, so put the blob directory on the same local filesystem as its parent, and give it room for every deploy you keep. Unreferenced blobs are removed by garbage collection (C1.8).

| Variable                    | Default              | Limit                   |
| --------------------------- | -------------------- | ----------------------- |
| `WARDLOCK_MAX_FILE_SIZE`    | `26214400` (25 MiB)  | bytes per file          |
| `WARDLOCK_MAX_DEPLOY_FILES` | `20000`              | files per deploy        |
| `WARDLOCK_MAX_DEPLOY_SIZE`  | `1073741824` (1 GiB) | bytes in a whole deploy |

Values are whole numbers of at least 1, without units. A deploy over a limit is rejected when it is created (`413 limit_exceeded`, naming the file where one is at fault), and an upload body longer than the per-file limit is rejected too. Raising the per-file limit means raising a reverse proxy's request body limit to match.

## Backups

Everything lives in `WARDLOCK_DATA_DIR`: `wardlock.db` and `blobs/` (or `WARDLOCK_BLOB_DIR` if set; and `certs/`, which can be re-issued).

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

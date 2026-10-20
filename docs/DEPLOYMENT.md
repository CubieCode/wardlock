# Deployment

Wardlock is alpha software. This document describes how to run an instance; a roadmap ID next to a section names the item that built it. Wardlock has not had an external security review, so do not use it to protect anything sensitive yet.

Wardlock needs one machine, one binary and a data directory. SQLite is embedded; there is no database server, cache or queue to run. Docker is optional.

## Quick start

The shortest path to a running instance. Each step links to the reference section with the detail.

1. Check the [requirements](#requirements) and generate a server key: `openssl rand -base64 32 | tr '+/' '-_'`. Keep a copy off the server.
2. Choose a base domain for sites and a dashboard host, and point a wildcard record and the dashboard host at the server ([Domains and DNS](#domains-and-dns)).
3. Choose who terminates TLS ([TLS](#tls)): Wardlock itself (`auto`, with a Cloudflare or deSEC DNS token) or a reverse proxy in front ([Behind Caddy or nginx](#behind-caddy-or-nginx)).
4. Install and start the server: [systemd](#single-binary-with-systemd), [Docker](#docker), or the ready-made [VPS deployment](#vps-with-automatic-tls-and-r2-o412) with automatic TLS and R2.
5. Configure [SMTP](#smtp) so sign-in and email codes reach people, and optionally [OAuth and OIDC](#oauth-and-oidc).
6. Open the one-time `setup_url` from the log to create the first owner.
7. Turn on the [operator listener](#health-readiness-and-metrics) for `/readyz` and `/metrics`, and schedule [backups](#backups).

## Requirements

- Linux (amd64 or arm64) or macOS; Windows is untested.
- A DNS zone you control, with a wildcard record for sites.
- Ports 80 and 443 reachable from the internet for automatic TLS, or a reverse proxy in front.
- An SMTP server for email codes (A2.4) and owner sign-in codes (C1.1).
- Persistent storage for `WARDLOCK_DATA_DIR`, on a local filesystem (not NFS, because of SQLite locking). Deploy file bodies live there too unless you put them in an [S3-compatible bucket](#s3-compatible-blob-storage).
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

Owners add their own hostnames to a site with `wardlock domain add` or the site's page in the dashboard; the operator sets nothing up per domain. The owner creates a `CNAME` from their hostname to `<name>.<base domain>`, which reaches the server through the sites wildcard. Keep the sites wildcard as A/AAAA records, and keep ports 80 and 443 reachable from the internet for HTTP-01/TLS-ALPN-01.

<details>
<summary>Apex domains, verification resolvers and the check schedule</summary>

Apex domains and CDN-proxied records use the TXT fallback described in [SPEC](SPEC.md#custom-domains-c19-d35) plus A/AAAA records (or ALIAS/flattening) pointing at the server, so publish the server's addresses to owners who need them.

Verification follows the owner's CNAME chain to its end and accepts it only when that end is `<name>.<base domain>` itself; if the wildcard were a CNAME to another name, every chain would end there and Wardlock could not tell which site a domain points at, so only the TXT record would verify.

| Variable                 | Default                | Meaning                                                                        |
| ------------------------ | ---------------------- | ------------------------------------------------------------------------------ |
| `WARDLOCK_DNS_RESOLVERS` | the system's resolvers | comma-separated `host:port` DNS servers for verification, such as `9.9.9.9:53` |

`WARDLOCK_DNS_RESOLVERS` chooses where verification lookups go, in every TLS mode. Use public or authoritative-facing resolvers if the local one caches aggressively or answers split-horizon records. Wardlock's own DNS client keeps no cache; each lookup has 10 seconds.

The server checks domains in the background (a pass every minute): a new domain straight away and then after 1, 2, 4 … 32 minutes and hourly for 48 hours, then only when its owner asks, until it is deleted 7 days after it was added if it is still pending (logged as `unverified custom domain expired`; the hostname can then be claimed again); active and lapsed domains every 24 hours. An active domain that fails every check for 72 hours lapses and stops being served; one that passes again is restored without the owner doing anything. Status changes are logged as `custom domain status changed` with the hostname, the old and new status and the failure reason; verification tokens are never logged. Changes reach the edge, the on-demand certificate decision and `/.wardlock/tls-ask` on the next request, and a domain that lapses or is removed (or whose site is torn down) has its certificate deleted from `<WARDLOCK_DATA_DIR>/certs` in automatic TLS mode.

</details>

## TLS

`WARDLOCK_TLS_MODE` chooses who terminates TLS:

| Mode    | Default when                                     | Behaviour                                                                                                                                                           |
| ------- | ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `local` | base domain and dashboard host are `*.localhost` | Plain HTTP on `WARDLOCK_HTTP_ADDR`. Browsers treat `http://*.localhost` as a secure context. Refused for any other host.                                            |
| `proxy` | any other host                                   | Plain HTTP on `WARDLOCK_HTTP_ADDR` behind a TLS-terminating proxy ([Behind Caddy or nginx](#behind-caddy-or-nginx)). URLs Wardlock prints use `https://`.           |
| `auto`  | never: set it explicitly                         | Wardlock obtains and renews its own certificates with ACME (certmagic) and serves HTTPS on `WARDLOCK_HTTPS_ADDR`; plain HTTP answers ACME challenges and redirects. |

A misconfiguration stops `wardlockd serve` with a message naming the variable.

<details>
<summary>Which misconfigurations stop the server</summary>

An unknown mode, `local` with public hosts, `auto` with `localhost` or IP hosts or without a DNS provider and token, or an `auto`-only variable (`WARDLOCK_ACME_*`, `WARDLOCK_DNS_*`) or `WARDLOCK_TRUSTED_PROXIES` set in a mode that ignores it.

</details>

### Automatic TLS

```sh
WARDLOCK_TLS_MODE=auto
WARDLOCK_BASE_DOMAIN=example-sites.net
WARDLOCK_DASHBOARD_HOST=wardlock.example.com
WARDLOCK_DNS_PROVIDER=cloudflare          # or desec
WARDLOCK_DNS_API_TOKEN=…                  # keep it in the env file, mode 0640
WARDLOCK_ACME_EMAIL=ops@example.com       # optional: expiry and account notices
```

Setting `auto` agrees to the CA's terms of service. Wardlock certifies the sites wildcard `*.<base domain>` (which also covers the auth origin) through DNS-01, the dashboard host, and verified custom domains on demand. Publish ports 80 and 443, and give the DNS token the narrowest scope the provider allows.

<details>
<summary>Variables</summary>

| Variable                 | Default                                          | Meaning                                                                                      |
| ------------------------ | ------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| `WARDLOCK_HTTP_ADDR`     | `:80` in auto mode                               | ACME HTTP-01 answers, `/healthz`, and a redirect to HTTPS for everything else                |
| `WARDLOCK_HTTPS_ADDR`    | `:443`                                           | Sites, the dashboard and the API over TLS (HTTP/2 and HTTP/1.1, TLS 1.2 or later)            |
| `WARDLOCK_ACME_CA`       | `https://acme-v02.api.letsencrypt.org/directory` | ACME directory. Use `https://acme-staging-v02.api.letsencrypt.org/directory` for a first try |
| `WARDLOCK_ACME_EMAIL`    | none                                             | ACME account contact                                                                         |
| `WARDLOCK_ACME_CA_ROOT`  | system roots                                     | PEM bundle to trust for the ACME server itself (a private CA or Pebble)                      |
| `WARDLOCK_DNS_PROVIDER`  | required                                         | `cloudflare` or `desec`                                                                      |
| `WARDLOCK_DNS_API_TOKEN` | required                                         | The provider's API token; never logged                                                       |

</details>

<details>
<summary>What Wardlock certifies, and how</summary>

- `*.<base domain>` through DNS-01. One wildcard covers every site and the auth origin `auth.<base domain>` (A2.5).
- The dashboard host: through DNS-01 with the same token when it is the base domain itself or deeper inside its zone (one label under it is already covered by the wildcard), otherwise through HTTP-01 or TLS-ALPN-01, so the token needs no access to the dashboard's zone.
- Custom domains (C1.9) on demand, at the first TLS handshake for the name, through HTTP-01 or TLS-ALPN-01, and only while the domain is verified and active. A name that has never been active (unverified or pending domains, nested names under the base domain, IP addresses) gets no certificate and the handshake fails. When a domain lapses or is removed (or its site is torn down), Wardlock drops its certificate from memory and deletes it from storage, so the next handshake asks the decision again and fails; the edge answers the host with the same 404 as an unknown host meanwhile.

DNS-01 records are created under `_acme-challenge.<base domain>` and removed after validation.

</details>

<details>
<summary>Ports, timeouts and certificate storage</summary>

ACME validation reaches the server on ports 80 and 443 from the internet, and the redirect and every URL Wardlock prints use the default HTTPS port, so publish ports 80 and 443. The listen addresses may differ when something forwards them, for example in Docker (`WARDLOCK_HTTP_ADDR=:8080`, `WARDLOCK_HTTPS_ADDR=:8443`, published as `80:8080` and `443:8443`); the systemd unit can bind 80 and 443 directly. Both listeners drop idle keep-alive connections after 120 seconds and slow request headers after 10 seconds; the plain-HTTP listener also limits whole requests and responses to 30 seconds. The HTTPS listener has no whole-request limit, so slow uploads and downloads of large deploy files still complete. Certificates obtain and renew in the background, retrying with backoff; failures are logged with the name and the CA's reason. Until the first wildcard arrives, handshakes for site hosts fail.

Certificates, keys and the ACME account live in `<WARDLOCK_DATA_DIR>/certs` (certmagic file storage). They are re-issued if lost, but keep them across restarts to stay within the CA's rate limits.

</details>

<details>
<summary>DNS token scopes for Cloudflare and deSEC</summary>

Give the token the narrowest scope the provider allows and nothing else:

- **Cloudflare:** an API token (not the global API key) with the permissions _Zone → DNS → Edit_ and _Zone → Zone → Read_, with _Zone Resources_ set to the sites zone only. Optionally restrict _Client IP Address Filtering_ to the server.
- **deSEC:** a token without permission to manage tokens. Restrict it with token policies: a default policy that denies writes, and one policy allowing writes to `TXT` records at subname `_acme-challenge` in the base domain (and at `_acme-challenge.<dashboard label>` if the dashboard host is deeper in that zone). Set _allowed subnets_ to the server's address.

</details>

<details>
<summary>Testing issuance with Pebble or Let's Encrypt staging</summary>

`internal/tls` tests run the whole flow against Pebble, Let's Encrypt's test CA, in process with a fake DNS server and DNS provider, as part of `pnpm check`; no Docker, network or credentials are involved. To try a real server without touching Let's Encrypt, run [Pebble](https://github.com/letsencrypt/pebble) with `pebble-challtestsrv` as its DNS server, point `WARDLOCK_ACME_CA` at Pebble's directory and `WARDLOCK_ACME_CA_ROOT` at its `test/certs/pebble.minica.pem`, and resolve your test names to the server through the challenge test server. A real DNS provider is needed for DNS-01 outside the tests, so use the Let's Encrypt staging directory with a throwaway zone instead when you want to check provider credentials.

</details>

## Install a release binary

Releases are not published yet; see the [launch checklist](LAUNCH-CHECKLIST.md). When they are, [`deploy/install.sh`](../deploy/install.sh) installs the binary for this machine (Linux or macOS) after checking the archive against `checksums.txt`; it refuses a missing or mismatched checksum and never runs `sudo` itself:

```sh
sh install.sh --version 0.1.0 --component wardlockd   # or wardlock (default), or all
sh install.sh --component all --dir ~/.local/bin      # latest release, no root
```

<details>
<summary>Release archives and installing from a mirror</summary>

Each GitHub release carries `wardlockd_<version>_<os>_<arch>` and `wardlock_<version>_<os>_<arch>` archives for linux, darwin and windows on amd64 and arm64 (`.zip` on Windows), `checksums.txt` (SHA-256 of every archive and SBOM) and an SPDX SBOM per archive (`<archive>.sbom.json`). The wardlockd archive also contains `wardlockd.service`.

`--base-url` installs from a mirror or a local snapshot directory instead, for example `--base-url file://$PWD/.local/goreleaser --version 0.0.1-snapshot.abc1234`.

</details>

## Single binary with systemd

```sh
sudo useradd --system --home /var/lib/wardlock --create-home wardlock
sudo sh deploy/install.sh --version 0.1.0 --component wardlockd
sudo install -m 0640 -o root -g wardlock wardlock.env /etc/wardlock/wardlock.env
sudo install -m 0644 deploy/wardlockd.service /etc/systemd/system/wardlockd.service
sudo systemctl daemon-reload && sudo systemctl enable --now wardlockd
```

On first start with no owners, the log shows a one-time setup URL: `journalctl -u wardlockd | grep setup_url`. Or run `sudo -u wardlock wardlockd admin create --email you@example.com` with the same environment as the service. Afterwards owners sign in with an emailed code or a passkey, and invite further owners from the dashboard.

<details>
<summary>The unit's hardening</summary>

[`deploy/wardlockd.service`](../deploy/wardlockd.service) runs as the `wardlock` user with `StateDirectory=wardlock` (so `WARDLOCK_DATA_DIR=/var/lib/wardlock`), `AmbientCapabilities=CAP_NET_BIND_SERVICE` for ports 80/443, and systemd hardening (`ProtectSystem=strict`, `NoNewPrivileges`, private `/tmp`, a `@system-service` system call filter, `UMask=0077`).

</details>

<details>
<summary>The first owner, sign-in URLs, passkeys and the sign-in email limit</summary>

The setup URL (`setup_url`) is valid 60 minutes and single use, with a new one on every start until an owner exists. `wardlockd admin create` creates the owner and prints a one-time sign-in URL valid for 60 minutes. Owners add passkeys on the dashboard's Passkeys page; invitation links are valid for 7 days. Setup, sign-in and invitation URLs use `https://<dashboard host>` with no port, since production dashboards are reached over HTTPS on the default port; only a `localhost` or `*.localhost` dashboard host gets `http://` and the listen port. Passkeys belong to the dashboard host: changing `WARDLOCK_DASHBOARD_HOST` (or reaching the dashboard at another address) makes existing passkeys unusable, so owners sign in by email code and add them again.

The per-address limit on sign-in emails (5 per hour) counts every request for that address, whoever sends it. Anyone who knows an owner's address can spend those sends, which delays that owner's sign-in by up to an hour; it cannot sign them in. Passkeys (C1.6) avoid this.

</details>

## Docker

[`deploy/Dockerfile`](../deploy/Dockerfile) packages the static `wardlockd` into a distroless image that runs as the `nonroot` user (uid 65532) with data in the `/data` volume; [`deploy/compose.yaml`](../deploy/compose.yaml) is an example. No images are published yet, so build a snapshot and point the example at it:

```sh
goreleaser release --snapshot --clean   # images ghcr.io/cubiecode/wardlock:<version>-amd64 and -arm64
export WARDLOCK_SECRET=$(openssl rand -base64 32 | tr '+/' '-_')   # keep it; see Backups
WARDLOCK_IMAGE=ghcr.io/cubiecode/wardlock:<version>-amd64 docker compose -f deploy/compose.yaml up -d
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9740/readyz      # the operator listener; see Health, readiness and metrics
```

<details>
<summary>Release images and the example's hardening</summary>

Release images will be multi-arch (`linux/amd64`, `linux/arm64`) at `ghcr.io/cubiecode/wardlock:<version>` and `:latest`. The example compose file has a read-only root filesystem, all capabilities dropped and one volume for data.

</details>

## VPS with automatic TLS and R2 (O4.12)

A ready-made deployment for one small VPS with a public IP address. Wardlock terminates TLS itself ([Automatic TLS](#automatic-tls)), deploy files go to Cloudflare R2 ([S3-compatible blob storage](#s3-compatible-blob-storage)), and a nightly restic backup goes to a second R2 bucket. Nothing else runs on the machine, and nothing here is provisioned for you; you run each step below. The files are in [`deploy/vps/`](../deploy/vps/README.md).

<details>
<summary>What the setup is, and the files it uses</summary>

The sites wildcard is certified through DNS-01 with Cloudflare, the dashboard and owners' custom domains on demand. There is no reverse proxy, no Cloudflare Tunnel and no Cloudflare proxying.

| File                                                                                                                              | Purpose                                                                                                                                                                          |
| --------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`compose.yaml`](../deploy/vps/compose.yaml)                                                                                      | `wardlockd` only, pinned by version; ports 80 and 443 published, the operator listener on `127.0.0.1:9740`, read-only root, all capabilities dropped, one volume `wardlock-data` |
| [`wardlock.env.example`](../deploy/vps/wardlock.env.example)                                                                      | the configuration, names and placeholders only; copy it to `wardlock.env`                                                                                                        |
| [`backup.sh`](../deploy/vps/backup.sh), [`wardlock-backup.service`, `wardlock-backup.timer`](../deploy/vps/wardlock-backup.timer) | the nightly backup and its systemd timer                                                                                                                                         |
| [`restic.env.example`](../deploy/vps/restic.env.example)                                                                          | the backup repository and its credentials; copy it to `/etc/wardlock/restic.env`                                                                                                 |

The worked example is an OVHcloud VPS-1 (2 vCores, 4 GB RAM, NVMe) in the Singapore or Sydney region, but nothing in the files is specific to a provider: any VPS with a public IPv4 address, and preferably an IPv6 address, works. Below, `example-sites.net` is the sites domain (a zone on Cloudflare), `wardlock.example.com` the dashboard, and `<account id>` your Cloudflare account ID.

</details>

### Server and firewall

1. Create the VPS from a current Debian or Ubuntu image with your SSH key, and install updates.
2. Install Docker Engine 27 or later with the Compose plugin from [Docker's repository](https://docs.docker.com/engine/install/), plus `git` and `restic` from the distribution.
3. Allow inbound TCP 22, 80 and 443 and nothing else. With `ufw`: `ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw enable`.

<details>
<summary>Why Docker 27, ufw and published ports, and OVHcloud notes</summary>

Docker 27 and later gives the Compose network its own private IPv6 subnet and forwards IPv6 connections with their source address, which per-IP limits and the [access log](#access-log) need. Docker's published ports bypass `ufw`, which is why `compose.yaml` publishes the operator listener on the host's loopback only.

On OVHcloud:

- The VPS has a public IPv4 and an IPv6 address, both shown in the control panel. Some images do not configure the IPv6 address; if `ip -6 addr` shows no global address, add the address and gateway from the control panel to the network configuration (netplan or cloud-init), and check from another machine with `ping -6 <server IPv6>` before publishing AAAA records.
- The optional Network Firewall in the control panel filters traffic before it reaches the VPS. If you enable it, allow TCP 22, 80 and 443 there too, or certificate validation and visitors never arrive.
- Reverse DNS is optional: Wardlock sends mail through your SMTP provider, not from the VPS.

</details>

### DNS records

In the Cloudflare dashboard for the sites zone (and wherever the dashboard's zone lives), create these records with the proxy status **DNS only** (grey cloud), never **Proxied**:

| Name                     | Type    | Content                               |
| ------------------------ | ------- | ------------------------------------- |
| `*.example-sites.net`    | A, AAAA | `<server IPv4>`, `<server IPv6>`      |
| `auth.example-sites.net` | A, AAAA | the same (the wildcard covers it too) |
| `wardlock.example.com`   | A, AAAA | the same                              |

<details>
<summary>Why DNS only, the wildcard and CAA records</summary>

Proxied records would make Cloudflare terminate TLS instead of Wardlock, hide visitors' addresses, and break certificate validation for the dashboard and custom domains.

Keep the wildcard as A/AAAA records, not a CNAME ([Custom domains](#custom-domains-c19)). If either zone has CAA records, allow `letsencrypt.org`. See [Domains and DNS](#domains-and-dns) for choosing the domains.

</details>

### Cloudflare API token

Create an API token (not the global API key) with _Zone → DNS → Edit_ and _Zone → Zone → Read_ on the sites zone only, as [Automatic TLS](#automatic-tls) describes, and optionally restrict it to the server's addresses. It goes in `WARDLOCK_DNS_API_TOKEN`. When the dashboard is in another zone, Wardlock certifies it with HTTP-01 or TLS-ALPN-01, so the token needs no access there.

### R2 buckets and tokens

1. In R2, create two private buckets, for example `wardlock-blobs` for deploy files and `wardlock-backups` for restic, with a location hint near the VPS. Turn on no public access, custom domain or `r2.dev` URL for either: every file is served through Wardlock's access control.
2. Create two R2 API tokens with _Object Read & Write_, each limited to one bucket: one for `wardlockd`, one for restic. Each shows an access key ID and a secret access key once; the backup token must not reach `wardlockd`, and the blobs token must not reach the backups.
3. The S3 endpoint for both is `https://<account id>.r2.cloudflarestorage.com`, the region `auto`.

### Configure and start

Until a release image is published, build one from source on the server (or anywhere, then `docker save` and `docker load`). The `source` target of [`deploy/Dockerfile`](../deploy/Dockerfile) builds the dashboard and `wardlockd` into the same distroless image as the release:

```sh
sudo git clone https://github.com/CubieCode/wardlock /opt/wardlock
cd /opt/wardlock
sudo docker build -f deploy/Dockerfile --target source -t wardlock:source .
echo WARDLOCK_IMAGE=wardlock:source | sudo tee deploy/vps/.env
```

Once releases exist, skip the build and leave `.env` out: `compose.yaml` names `ghcr.io/cubiecode/wardlock:<version>`. Then configure:

```sh
cd /opt/wardlock/deploy/vps
sudo install -m 0600 wardlock.env.example wardlock.env
openssl rand -base64 32 | tr '+/' '-_'   # WARDLOCK_SECRET; store a copy off the server
sudoedit wardlock.env
```

Fill in `WARDLOCK_SECRET`, the hosts, the Cloudflare token, the R2 bucket, endpoint and blobs token, and [SMTP](#smtp); add [OAuth and OIDC](#oauth-and-oidc) providers if you want them. The example already turns on the [blob cache](#blob-cache) (2 GiB in the data volume) and daily garbage collection, so R2 sees few requests; change the cache size to suit the disk. For the first start, uncomment the Let's Encrypt staging `WARDLOCK_ACME_CA` so mistakes do not use up production rate limits. Then:

```sh
sudo docker compose up -d
sudo docker compose logs wardlockd | grep -E 'setup_url|certificate|error'
curl -s http://127.0.0.1:9740/readyz      # ready once the database, R2 and certificates pass
```

When `/readyz` reports ready, comment out `WARDLOCK_ACME_CA` again and run `sudo docker compose up -d` to obtain production certificates. Then open the `setup_url` from the log to create the first owner.

<details>
<summary>Certificates on first start, and creating the first owner</summary>

`/readyz` reports `certificates` failing until the wildcard and dashboard certificates arrive, usually within a minute or two; the log names the CA's reason when one fails. `sudo docker compose up -d` after the change recreates the container; staging and production certificates are stored apart under `certs/certificates/`, one folder per CA. The `setup_url` (`https://wardlock.example.com/…`) is valid 60 minutes, with a new one on every start until an owner exists, as [Single binary with systemd](#single-binary-with-systemd) describes. `sudo docker compose exec wardlockd wardlockd admin create --email you@example.com` works too.

</details>

### Custom domains on the VPS

Owners add their own domains from the dashboard or the CLI and point them at the server ([Custom domains](#custom-domains-c19)); you set nothing up per domain. Wardlock obtains each certificate on demand at the first TLS handshake once the domain is verified and active.

<details>
<summary>Let's Encrypt rate limits and other CAs</summary>

Every certificate counts against [Let's Encrypt's rate limits](https://letsencrypt.org/docs/rate-limits/) (per registered domain, per account and for failed validations), which matter mostly when many owners add domains at once or one keeps failing validation; renewals do not count against new-certificate limits. If you reach a limit, or Let's Encrypt is unavailable for a long time, `WARDLOCK_ACME_CA` can point Wardlock at another ACME CA's directory. It must issue without external account binding, which Wardlock does not support.

</details>

### Backups and restore on the VPS

[`backup.sh`](../deploy/vps/backup.sh) runs nightly as root: `wardlockd backup` copies the database while the server runs, restic uploads the data volume to the `wardlock-backups` bucket, and old snapshots are pruned. Deploy files are in R2 and are not in this backup, and neither is `WARDLOCK_SECRET`: store it, with the restic password, somewhere that survives losing the VPS.

<details>
<summary>What the backup does</summary>

The data volume holds `wardlock.db`, `certs/` (certificates, keys and the ACME account) and `blob-cache/` (local copies of deploy files, see [Blob cache](#blob-cache)); deploy files are in R2.

1. `wardlockd backup` writes a consistent copy of the database into the volume, while the server runs ([Backups](#backups)).
2. restic uploads the whole volume to the `wardlock-backups` bucket: the copy and `certs/`. The live `wardlock.db`, `wardlock.db-wal` and `wardlock.db-shm` are left out, since they cannot be copied safely while the server runs and the copy replaces them, and so is `blob-cache/`, which refills from R2.
3. The copy is deleted, and snapshots older than 14 daily, 8 weekly and 12 monthly are pruned.

</details>

Set it up once:

```sh
sudo install -d -m 0700 /etc/wardlock
sudo install -m 0600 /opt/wardlock/deploy/vps/restic.env.example /etc/wardlock/restic.env
sudoedit /etc/wardlock/restic.env                       # repository, backup token
openssl rand -base64 32 | sudo tee /etc/wardlock/restic-password >/dev/null
sudo chmod 0600 /etc/wardlock/restic-password
sudo sh -c 'set -a; . /etc/wardlock/restic.env; restic init'
sudo install -m 0644 /opt/wardlock/deploy/vps/wardlock-backup.service \
  /opt/wardlock/deploy/vps/wardlock-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now wardlock-backup.timer
sudo systemctl start wardlock-backup.service && journalctl -u wardlock-backup.service -n 5
```

<details>
<summary>Cron instead of a timer, secrets and the deploy files bucket</summary>

Without systemd timers, a cron line does the same: `30 3 * * * root /opt/wardlock/deploy/vps/backup.sh` in `/etc/cron.d/wardlock-backup`.

**Keep the secrets out of the backup.** `WARDLOCK_SECRET` lives in `wardlock.env`, outside the volume, so it is never in the backup. Store it, together with the restic password and both env files' credentials, somewhere that survives losing the VPS and is not the backup bucket: a password manager or an offline copy that only operators can reach. Without `WARDLOCK_SECRET` a restore loses every share link, API token and session; without the restic password the backups cannot be read at all.

**Deploy files are not in this backup.** Protect the `wardlock-blobs` bucket as [Backups](#backups) describes for blobs in a bucket, for example with an `rclone sync` to another bucket after each database backup.

</details>

To restore, on the same or a new VPS prepared as above, with `wardlock.env` (the same `WARDLOCK_SECRET` and hosts), `/etc/wardlock/restic.env` and the restic password back in place:

```sh
cd /opt/wardlock/deploy/vps
sudo docker compose down                                # if it is running
sudo docker compose up --no-start                       # creates the volume
data=$(sudo docker volume inspect --format '{{ .Mountpoint }}' wardlock-data)
sudo sh -c 'set -a; . /etc/wardlock/restic.env; restic snapshots; restic restore latest --host wardlock --target /root/wardlock-restore'
src=/root/wardlock-restore$data                         # restic keeps the original path
sudo find "$data" -mindepth 1 -delete                   # an empty data directory
db=$(sudo find "$src" -maxdepth 1 -name 'wardlock-*.db' | sort | tail -n 1)
sudo cp "$db" "$data/wardlock.db"
sudo test -d "$src/certs" && sudo cp -a "$src/certs" "$data/"   # certificates, if any
sudo chown -R 65532:65532 "$data" && sudo chmod 0700 "$data"
sudo docker compose up -d && curl -s http://127.0.0.1:9740/readyz
```

`restic restore` puts files under the target at their original path; on a host with a different Docker data root, `restic ls latest` shows where. The rest of [Restore](#restore) applies: migrations upgrade an older backup, and anything written after the backup is lost.

### Upgrades on the VPS

Run the backup first (`sudo systemctl start wardlock-backup.service`). Then change the version in `compose.yaml` (or in `WARDLOCK_IMAGE` in `.env`) and run `sudo docker compose pull && sudo docker compose up -d`. For a source build, check out the new commit, rebuild the `source` image and run `sudo docker compose up -d`. Migrations run on start and are forward-only ([Upgrades](#upgrades)): going back needs the backup.

## Behind Caddy or nginx

If something else already terminates TLS on the machine, run Wardlock in proxy mode (`WARDLOCK_TLS_MODE=proxy`, the default for non-`localhost` hosts) on a loopback port and proxy every site host and the dashboard to it, preserving the `Host` header. Examples: [`deploy/Caddyfile.example`](../deploy/Caddyfile.example), [`deploy/nginx.conf.example`](../deploy/nginx.conf.example). The proxy must:

- pass `Host` unchanged, set `X-Forwarded-For` and `X-Forwarded-Proto`, and be listed in `WARDLOCK_TRUSTED_PROXIES` (`127.0.0.1,::1` for a proxy on the same machine);
- not cache protected responses, and not log query strings (they carry share-link tokens), or redact the `t` parameter;
- allow request bodies of at least the per-file limit (25 MiB default) for blob uploads;
- for custom domains, obtain their certificates: with Caddy, on-demand TLS asking `http://127.0.0.1:8080/.wardlock/tls-ask`. nginx cannot.

The examples do all of this.

<details>
<summary>Trusted proxies and forwarding headers</summary>

`WARDLOCK_TRUSTED_PROXIES` takes the proxy's addresses, comma-separated IP addresses or CIDR ranges. A range covering every address (`0.0.0.0/0`, `::/0`) is refused, and IPv4-mapped forms (`::ffff:10.0.0.1`) are read as IPv4. Wardlock believes forwarding headers only from those peers: it reads `X-Forwarded-For` from the right, skipping trusted hops, so the first untrusted address is the client and anything a client prepends is ignored; it keeps `X-Forwarded-Proto` only when it is `http` or `https`; and it removes `X-Forwarded-*`, `X-Real-IP` and `Forwarded` from every other peer. `X-Forwarded-Host` is never used: routing is by `Host`.

Without `WARDLOCK_TRUSTED_PROXIES` (Wardlock logs a warning at startup), Wardlock sees every client behind the proxy as the proxy's address, so every per-IP limit (owner and visitor sign-in, share-link guesses, API-token failures and CLI login) becomes instance-wide: one busy or hostile client can make sign-in wait for everyone until the window resets. Never list an address that untrusted clients can connect from, or they can choose their own address.

</details>

<details>
<summary>Caching immutable assets, and body limits</summary>

A deploy made with `--immutable-assets` sends `Cache-Control: public, max-age=31536000, immutable` for its hashed files under `/assets/` on public sites (SPEC "Serving"), so a caching proxy or CDN in front may keep them for a year; everything else revalidates. Protected sites send `private` and must still not be cached at the proxy.

Raising the per-file limit means raising the proxy's request body limit to match.

</details>

<details>
<summary>Custom domain certificates through the proxy (<code>/.wardlock/tls-ask</code>)</summary>

With Caddy, use on-demand TLS with `ask` set to `http://127.0.0.1:8080/.wardlock/tls-ask`, so it only requests certificates for hostnames Wardlock has verified, and a catch-all `https://` site with `tls { on_demand }`, as the example does. `/.wardlock/tls-ask?domain=<hostname>` answers 200 for an active custom domain and 404 for anything else, and only to a peer in `WARDLOCK_TRUSTED_PROXIES` (any `Host`) whose request carries no `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host`, `Forwarded` or `X-Real-IP` header: Caddy's own `ask` request has none, while every visitor request it forwards does. Every other request, and every TLS mode but proxy, gets 404. As a second guard, make the public site blocks answer `/.wardlock/tls-ask` themselves with a 404 before proxying, as the examples do (Caddy: `@tlsask path /.wardlock/tls-ask*` with `respond @tlsask 404`; nginx: `location ^~ /.wardlock/tls-ask { return 404; }`). Caddy keeps a certificate it already holds for a domain that later lapses or is removed until renewal, but Wardlock answers that host with the unknown-host 404. nginx cannot issue certificates on demand; use Caddy, or manage custom-domain certificates yourself.

</details>

## SMTP

`WARDLOCK_SMTP_URL` (F0.4) chooses how mail leaves the server:

- `smtp://user:pass@host:587` connects in plain text and upgrades with STARTTLS before authenticating.
- `smtps://user:pass@host:465` uses TLS from the first byte.
- `dev://` (the default) delivers nothing and writes each message to `.local/mail/`. Use it for development only.

`WARDLOCK_SMTP_FROM` sets the sender (`Wardlock <no-reply@example.com>`). Configure SPF, DKIM and DMARC for the sending domain or codes will land in spam.

<details>
<summary>STARTTLS, ports, certificates, credentials and the dev mailer</summary>

- `smtp://`: a server that does not offer STARTTLS is refused; there is no option to send in plain text. The port defaults to 587.
- `smtps://`: the port defaults to 465.
- `dev://` writes each message to `.local/mail/` (relative to the working directory) as a readable `.eml` file and logs only the recipient, subject and file name, never the body or code. Outside local mode Wardlock logs a warning at startup (`WARDLOCK_SMTP_URL is dev:// outside local mode`).

The server certificate is always verified against the system roots. Percent-encode special characters in the user name and password (`@` is `%40`). Credentials never appear in logs or errors, which show the server as `smtp://[redacted]@host:port`. `WARDLOCK_SMTP_FROM` defaults to `Wardlock <wardlock@<dashboard host>>`.

</details>

## OAuth and OIDC

Visitors can sign in to protected sites with GitHub, Google or one generic OpenID Connect provider (A2.5), once the operator registers Wardlock with the provider and an owner enables the OAuth method on a policy with an allowlist. Each provider is configured when its client ID is set; the secret is then required, and setting only one of the pair is a startup error. None configured leaves the method unavailable: enabling it is refused.

| Variable                                                     | Meaning                                                                                          |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------ |
| `WARDLOCK_GITHUB_CLIENT_ID`, `WARDLOCK_GITHUB_CLIENT_SECRET` | A GitHub OAuth app (not a GitHub App)                                                            |
| `WARDLOCK_GOOGLE_CLIENT_ID`, `WARDLOCK_GOOGLE_CLIENT_SECRET` | A Google Cloud OAuth client of type "Web application"                                            |
| `WARDLOCK_OIDC_ISSUER`                                       | The issuer URL (`https://`; `http://` only for `localhost`), whose discovery document is fetched |
| `WARDLOCK_OIDC_CLIENT_ID`, `WARDLOCK_OIDC_CLIENT_SECRET`     | The client registered with that issuer (confidential, authorisation code flow)                   |
| `WARDLOCK_OIDC_NAME`                                         | The button label, as in "Continue with …"; default `single sign-on`, at most 40 characters       |

Register exactly one redirect (callback) URL per provider on the auth origin: `https://auth.<base domain>/callback/github`, `/callback/google` or `/callback/oidc` (in local mode `http://auth.wardlock.localhost:<port>/callback/…`).

<details>
<summary>The auth origin, scopes, verified email and sign-ins in flight</summary>

Providers never redirect to sites themselves, so one registration covers every site. The auth origin is covered by the sites' wildcard DNS record and certificate; `WARDLOCK_DASHBOARD_HOST` cannot be the auth origin, and `auth` cannot be a site name.

Wardlock asks GitHub for `user:email`, plus `read:org` only when the policy lists organisations; for an organisation with OAuth app access restrictions, an organisation owner must approve the app before memberships count. OIDC providers (Google included) are asked for `openid email`; an account counts only when the ID token (or the userinfo endpoint) says `email_verified` is true, so a provider that never asserts verification cannot be used. Discovery runs on the first sign-in, not at startup, so a provider outage does not stop the server.

Sign-ins in progress (up to 10 minutes at the provider) and the 60-second codes that hand them to sites are kept in memory: a restart drops the sign-ins in flight, and visitors simply sign in again. Behind a reverse proxy, keep `code` and `state` out of access logs as Wardlock does in its own.

</details>

## Deploy storage and limits

Deploy file bodies are stored once per content hash under `WARDLOCK_BLOB_DIR` (default `<WARDLOCK_DATA_DIR>/blobs`), or in an [S3-compatible bucket](#s3-compatible-blob-storage). Give the storage room for every deploy you keep; unreferenced blobs are removed by [garbage collection](#garbage-collection).

| Variable                    | Default              | Limit                   |
| --------------------------- | -------------------- | ----------------------- |
| `WARDLOCK_MAX_FILE_SIZE`    | `26214400` (25 MiB)  | bytes per file          |
| `WARDLOCK_MAX_DEPLOY_FILES` | `20000`              | files per deploy        |
| `WARDLOCK_MAX_DEPLOY_SIZE`  | `1073741824` (1 GiB) | bytes in a whole deploy |

<details>
<summary>On-disk layout and how limits are enforced</summary>

The blob directory is laid out as `sha256/ab/cdef…`. Each upload is written to a temporary file in the same directory, fsynced and renamed into place, so put the blob directory on the same local filesystem as its parent.

Values are whole numbers of at least 1, without units. A deploy over a limit is rejected when it is created (`413 limit_exceeded`, naming the file where one is at fault), and an upload body longer than the per-file limit is rejected too. Raising the per-file limit means raising a reverse proxy's request body limit to match.

</details>

### S3-compatible blob storage

With `WARDLOCK_BLOB_BACKEND=s3` the file bodies go to a bucket on any S3-compatible service (AWS S3, Cloudflare R2, MinIO, Backblaze B2, Wasabi, Garage) instead of `WARDLOCK_BLOB_DIR`, which must then be unset. Everything else stays on the machine: the database, rate limits and certificates are still local, so this is still one server, not a cluster. Keep the bucket private: every file is served through Wardlock's access control. Add the [blob cache](#blob-cache) and run garbage collection daily to keep requests to the bucket down.

```sh
WARDLOCK_BLOB_BACKEND=s3
WARDLOCK_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com
WARDLOCK_S3_BUCKET=wardlock-blobs
WARDLOCK_S3_REGION=auto
AWS_ACCESS_KEY_ID=…
AWS_SECRET_ACCESS_KEY=…
```

<details>
<summary>Variables and addressing</summary>

The settings are checked at start, and a missing or malformed one stops the server with an error naming it.

| Variable                 | Default    | Meaning                                                                                                                              |
| ------------------------ | ---------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `WARDLOCK_BLOB_BACKEND`  | `fs`       | `fs` (the blob directory) or `s3`                                                                                                    |
| `WARDLOCK_S3_ENDPOINT`   | (required) | the service's base URL, scheme and host only: `https://s3.eu-west-1.amazonaws.com`, `https://<account>.r2.cloudflarestorage.com`     |
| `WARDLOCK_S3_BUCKET`     | (required) | bucket name; create the bucket first, Wardlock never does                                                                            |
| `WARDLOCK_S3_REGION`     | (required) | the bucket's region: `eu-west-1` on AWS, `auto` on R2, `us-east-1` on MinIO unless configured otherwise, `garage` on Garage          |
| `WARDLOCK_S3_PREFIX`     | none       | key prefix such as `wardlock/production`, so one bucket can hold other data                                                          |
| `WARDLOCK_S3_PATH_STYLE` | automatic  | `true` puts the bucket in the path (`https://endpoint/bucket/key`, usual for MinIO and Garage), `false` in the host (virtual-hosted) |

Without `WARDLOCK_S3_PATH_STYLE` the client uses virtual-hosted addressing for AWS endpoints and path style for everything else. Use plain `http://` only for a service on the same host or private network.

</details>

<details>
<summary>Credentials and least-privilege permissions</summary>

Credentials come from the standard AWS sources, in this order: `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` (with `AWS_SESSION_TOKEN` for temporary keys) in the environment; the shared credentials and config files (`AWS_SHARED_CREDENTIALS_FILE`, `AWS_CONFIG_FILE`, `AWS_PROFILE`; by default `~/.aws/credentials` of the service user); then a container, web-identity or EC2 instance role. Put keys in the service's environment file (mode 0600) like `WARDLOCK_SECRET`. They are never logged: the startup line `wardlockd started` names the endpoint, bucket, region, prefix and which credential source is used, and nothing else.

Give the credentials only what Wardlock needs, scoped to the prefix where the service allows it: `s3:ListBucket` on the bucket (readiness and garbage collection), and `s3:GetObject`, `s3:PutObject` and `s3:DeleteObject` on `<bucket>/<prefix>*`. For AWS:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::wardlock-blobs"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::wardlock-blobs/wardlock/production/*"
    }
  ]
}
```

Keep the bucket private (no public access, no website hosting): every file is served through Wardlock's access control, and anyone who can read the bucket can read every protected site. Wardlock never creates presigned URLs.

</details>

<details>
<summary>Object layout, upload integrity and size limit</summary>

Blobs are stored at `<prefix>sha256/ab/cdef…`, the same layout as on disk. Every upload is signed with the file's expected SHA-256 as its payload hash, and Wardlock hashes the bytes as they stream, failing the request before its last bytes are sent if they do not match, so a blob is either complete or absent.

How an upload reaches its key depends on whether the service itself rejects a body that does not match its signed payload hash. Once per server process, before the first upload, Wardlock asks: it uploads a small body to a temporary key `<prefix>uploads/<random>` signed with a deliberately wrong hash.

- **Rejected** with a checksum error (AWS S3 and MinIO do this): each later upload is one `PUT` straight to its final key.
- **Accepted**, or refused for another reason: the probe object is deleted, and each upload streams to a temporary key, is verified, is copied to its final key, and the temporary key is deleted. Unverified content never reaches a final key.
- **No answer** (rejected credentials, a server error or a network failure): that upload fails and the next one probes again.

The choice is logged once as `s3 upload path` with `path` (`direct` or `temporary key`) and the reason; it never depends on the provider's name and never logs credentials. Abandoned temporary keys older than `WARDLOCK_GC_GRACE_HOURS` are removed by garbage collection, as are blob objects with no database record. Each file is at most 5 GiB, the limit of one upload or copy, so `WARDLOCK_MAX_FILE_SIZE` may not exceed `5368709120`.

Serving takes each file's size from the deploy's manifest, so opening a file makes no request. Wardlock reads with ranged `GET`s from the position the response needs, so a `Range` request never downloads the whole object. A file missing from the bucket shows up on that first read and answers `500`, logged as `open blob`, before any of the response is sent. A `HEAD` request reads nothing, so it answers `200` even for a missing file.

</details>

<details>
<summary>Requests per deploy, page view and collection (for estimating a bill)</summary>

- **Deploys:** one `HEAD` per manifest file the owner has uploaded before, when the deploy is created and again when it is activated. Each uploaded file is one `PUT` on a service that checks payload hashes, otherwise one `PUT`, one `COPY` and one `DELETE`. Either way, an upload makes the same requests when the content is already stored, so upload timing does not reveal what other owners have stored. Activation also reads each HTML file once (no request if the [blob cache](#blob-cache) holds it) and stores its Markdown and text renditions the same way.
- **Upload probe:** one `PUT` per server process before its first upload, plus one `DELETE` when the service accepts the probe.
- **Serving:** one `GET` for each response that sends a body: a full file, a `Range`, or a `404.html`. With the [blob cache](#blob-cache), a full response costs one `GET` the first time and none after; a `Range` the cache does not hold costs one ranged `GET`. A `304 Not Modified`, a `412`, a `416` or a `HEAD` request costs none, with or without the cache. Every byte served from the bucket is egress, so run Wardlock close to it, or pick a service without egress fees.
- **Garbage collection:** one `LIST` per 1,000 stored objects each run, plus one `DELETE` per removed blob. With S3, run it daily (`WARDLOCK_GC_INTERVAL_MINUTES=1440`) rather than at the hourly default: unreferenced blobs wait out a 24-hour grace period anyway, so hourly runs mostly repeat the listing.
- **Readiness:** one `HEAD` of the bucket per `/readyz` or `/metrics` scrape.

Each file the cache does not hold waits for at least one round trip to the bucket, so expect slower first bytes than from a local disk.

</details>

### Blob cache

With S3, set `WARDLOCK_BLOB_CACHE_DIR` and `WARDLOCK_BLOB_CACHE_MAX_BYTES` to keep local copies of the files Wardlock serves. A cached file costs the bucket no request and no egress. The cache is off by default, and both variables are set together or not at all.

```sh
WARDLOCK_BLOB_CACHE_DIR=/var/cache/wardlock/blobs
WARDLOCK_BLOB_CACHE_MAX_BYTES=2147483648 # 2 GiB
```

<details>
<summary>How the cache fills, evicts and stays correct</summary>

| Variable                        | Default | Meaning                                                                            |
| ------------------------------- | ------- | ---------------------------------------------------------------------------------- |
| `WARDLOCK_BLOB_CACHE_DIR`       | none    | directory for cached copies; must not be, contain or sit inside the blob directory |
| `WARDLOCK_BLOB_CACHE_MAX_BYTES` | none    | bytes the cache keeps, a whole number of at least 1 without units                  |

- **Content-addressed.** Copies are stored by SHA-256 (`sha256/ab/cdef…` under the directory), so a copy can never be stale: a new deploy's changed file has a new hash.
- **Filled while serving.** A response that reads a whole file from the start (a full `GET`, or `Range: bytes=0-`, which browsers send for media) streams it to a temporary file under `fill/` as it is sent. The copy is kept only when its bytes hash to the file's SHA-256 at its size. A `Range` from anywhere else, or one that stops early, fills nothing, so it fetches only the bytes it asked for. Reading the rest of the object just to fill the cache would pay for bytes nobody requested.
- **One fill at a time.** Concurrent first requests for the same file all read from the bucket; one of them fills the cache.
- **Least recently used out.** Beyond the size limit, the copies used longest ago are deleted. A file bigger than the limit is never cached. Use is tracked in memory and stamped on each copy's modification time, so a restart rebuilds the order from the directory and removes unfinished fills.
- **Safe to wipe.** Deleting the directory, or any copy in it, while the server runs only means those files are fetched and cached again. A copy whose size is wrong is treated as missing.
- **Garbage collection** removes a blob's cached copy along with the blob, including one still filling. `wardlockd gc` removes copies from the same directory when it shares the server's configuration.

The cache works with the filesystem backend too, but there it only duplicates the blob directory, so it is meant for S3. Size it to hold the files visitors fetch most; one page view usually needs its HTML, stylesheets, scripts and images.

</details>

### Garbage collection

The server prunes deploy history and removes file bodies no deploy references at start and then every `WARDLOCK_GC_INTERVAL_MINUTES` (hourly by default). Each site keeps its newest `WARDLOCK_DEPLOY_HISTORY` deploys (50) plus its active deploy, so rollback works within that history. `wardlockd gc` runs one collection with the same settings and prints what it removed.

<details>
<summary>Variables, grace period, running it from cron and its log line</summary>

The active deploy is never pruned. Deploys not yet activated and bodies uploaded within `WARDLOCK_GC_GRACE_HOURS` are kept, so a slow upload is never cut short; keep the grace period longer than your longest deploy. `wardlockd gc` is safe to run while the server is running, for example from cron with `WARDLOCK_GC_INTERVAL_MINUTES=0`. With S3 blob storage, a daily run (`WARDLOCK_GC_INTERVAL_MINUTES=1440`) is enough and lists the bucket 24 times less often.

| Variable                       | Default | Meaning                                                               |
| ------------------------------ | ------- | --------------------------------------------------------------------- |
| `WARDLOCK_GC_INTERVAL_MINUTES` | `60`    | minutes between collections in the server, 0 to 525600; 0 runs none   |
| `WARDLOCK_GC_GRACE_HOURS`      | `24`    | hours that new uploads and pending deploys are protected, 1 to 8760   |
| `WARDLOCK_DEPLOY_HISTORY`      | `50`    | newest deploys kept per site, 1 to 100000; the active one is kept too |

Every run logs `garbage collection finished` with `deploys_pruned`, `blobs_deleted`, `files_deleted` (stored files with no database record, left by a crash mid-upload, and abandoned temporary uploads) and `bytes_freed`, or `garbage collection failed` with the error; a failed run is retried at the next interval.

</details>

## Access log

Every request for a published site's content (A2.8) adds one entry to the access log in the database: time, site, host, path, status, how the visitor got in (`public`, `link`, `password`, `email`, `oauth`, or `none` when refused), the share link's public ID, a truncated client IP and the User-Agent. Health checks, `/.wardlock/` pages, the dashboard and hosts with nothing published are not logged. The access log never holds a share link, and entries are kept for 90 days by default.

<details>
<summary>Variables</summary>

| Variable                             | Default | Meaning                                                          |
| ------------------------------------ | ------- | ---------------------------------------------------------------- |
| `WARDLOCK_ACCESS_LOG_IPV4_PREFIX`    | `24`    | leading bits of an IPv4 address kept, 0 to 32; 0 stores no IP    |
| `WARDLOCK_ACCESS_LOG_IPV6_PREFIX`    | `48`    | leading bits of an IPv6 address kept, 0 to 128; 0 stores no IP   |
| `WARDLOCK_ACCESS_LOG_RETENTION_DAYS` | `90`    | days to keep entries, 0 to 36500; 0 keeps them forever           |
| `WARDLOCK_ACCESS_LOG_BUFFER`         | `10000` | entries that may wait for a write, 1 to 1000000, before dropping |

</details>

<details>
<summary>Which addresses, paths and tokens are stored</summary>

The defaults keep enough of an address to tell networks apart, not individual households; set both prefixes to their maximum to keep full addresses, or to 0 to store none. IPv4-mapped IPv6 addresses count as IPv4. The IP is the client's address after trusted-proxy resolution: behind a reverse proxy listed in `WARDLOCK_TRUSTED_PROXIES` it is the client from `X-Forwarded-For`; without that setting every entry carries the proxy's address. Paths are stored without their query string, and any token in the path or User-Agent keeps only its public ID, so the access log never holds a share link.

</details>

<details>
<summary>Batched writes, dropped entries and retention</summary>

Entries are written in batches off the request path: serving never waits on the database. If the database falls behind and the buffer fills, new entries are dropped rather than slowing visitors; the server logs a warning (`access log entries dropped`, at most every 10 seconds) with the running total, and a failed batch write is logged and counted the same way. On shutdown the server writes what is buffered within the shutdown timeout. Retention runs at start and hourly beside the writer, deleting entries older than the retention period (including those of deleted sites) 1,000 at a time so the database is never locked for long.

</details>

## Health, readiness and metrics

`/healthz` answers `{"status":"ok"}` on every host of the main listener (and the plain-HTTP listener in automatic TLS mode) for as long as the process runs: use it for liveness. Readiness and metrics are served only on a separate operator listener, which is off until `WARDLOCK_OPERATOR_ADDR` names an address:

| Variable                 | Default            | Meaning                                                                           |
| ------------------------ | ------------------ | --------------------------------------------------------------------------------- |
| `WARDLOCK_OPERATOR_ADDR` | none (no listener) | listen address for `/healthz`, `/readyz` and `/metrics`, such as `127.0.0.1:9740` |

The operator listener is plain HTTP without authentication, so bind it to loopback or a private network that only the monitoring system reaches, and never proxy it to the internet. `GET /readyz` answers `200` when the database, blob storage and (in automatic TLS mode) certificates pass, and `503` otherwise; `GET /metrics` is Prometheus text. Alert on `wardlock_ready == 0`, a rising `wardlock_access_log_dropped_total`, and certificates within 14 days of expiry.

<details>
<summary>The operator listener's rules</summary>

It must differ from `WARDLOCK_HTTP_ADDR` and `WARDLOCK_HTTPS_ADDR`. It answers on any `Host` and serves nothing else (`404`); site, dashboard and unknown hosts on the main listeners never reach it, so `/metrics` and `/readyz` there are ordinary site paths or the dashboard. Its requests are neither logged nor counted in the request metrics.

</details>

<details>
<summary>Readiness checks, timeouts and shutdown</summary>

**`GET /readyz`** runs the checks concurrently and answers `200` with `{"status":"ready","checks":{"database":"ok","blobs":"ok"}}` when all pass, or `503` with `"status":"unready"` and the failing check's reason in place of `ok`. The checks: `database` reads from the database; `blobs` creates and removes a probe file (`.ready-*`) in `WARDLOCK_BLOB_DIR`, so a missing, read-only or full blob directory fails it, or with the S3 backend sends `HEAD` to the bucket, so an unreachable service, a missing bucket or rejected credentials fail it; `certificates` (automatic TLS mode only) needs an unexpired certificate for every managed name (the sites wildcard and the dashboard host), so it fails until the first certificates arrive and if renewal falls behind until expiry. A check that has not finished within 5 seconds reports `timeout`, so `/readyz` and `/metrics` answer promptly even when, say, the blob directory sits on a hung network mount; it keeps running in the background, and later probes wait on that same run rather than starting another. Once graceful shutdown begins (`SIGTERM` or `SIGINT`), `/readyz` answers `503` with `"status":"shutting_down"` without running the checks; the operator listener is the last to stop, so a load balancer sees the drain while in-flight requests finish (up to 10 seconds). A check that starts failing is logged once as `readiness check failing` with `check` and `error`, and `readiness check recovered` when it passes again.

</details>

<details>
<summary>Metrics</summary>

**`GET /metrics`** is the Prometheus text format (0.0.4). Labels never name hosts, paths, sites or owners:

| Metric                                                                                         | Type           | Meaning                                                                        |
| ---------------------------------------------------------------------------------------------- | -------------- | ------------------------------------------------------------------------------ |
| `wardlock_build_info{version}`                                                                 | gauge          | always 1                                                                       |
| `wardlock_start_time_seconds`                                                                  | gauge          | process start, Unix seconds                                                    |
| `wardlock_http_requests_total{code}`                                                           | counter        | requests on the main listeners by status class (`2xx` … `5xx`)                 |
| `wardlock_http_requests_in_flight`                                                             | gauge          | requests being answered                                                        |
| `wardlock_http_request_duration_seconds`                                                       | histogram      | time to answer, 5 ms to 10 s buckets                                           |
| `wardlock_ready{check}`                                                                        | gauge          | 1 when the readiness check passes, 0 when it fails (checks run at each scrape) |
| `wardlock_access_log_dropped_total`                                                            | counter        | access-log entries dropped since start ([Access log](#access-log))             |
| `wardlock_database_size_bytes`                                                                 | gauge          | `wardlock.db` plus its write-ahead log                                         |
| `wardlock_certificate_expiry_timestamp_seconds{name}`                                          | gauge          | automatic TLS only: expiry of each managed certificate, Unix seconds           |
| `go_goroutines`, `go_memstats_heap_alloc_bytes`, `go_memstats_sys_bytes`, `go_gc_cycles_total` | gauge, counter | Go runtime                                                                     |

</details>

<details>
<summary>Scraping and alerting with Prometheus, systemd and Compose</summary>

A Prometheus scrape job needs nothing more than the address: `static_configs: [{targets: ['127.0.0.1:9740']}]`. Alert on `wardlock_ready == 0`, a rising `wardlock_access_log_dropped_total`, and `wardlock_certificate_expiry_timestamp_seconds - time() < 14 * 86400` (renewal starts well before that). For systemd add `WARDLOCK_OPERATOR_ADDR=127.0.0.1:9740` to the env file; the Docker example sets `:9740` inside the container and publishes it on the host's loopback only. Inside Compose, `:9740` listens on the container's network interface, so every other container on the same Docker network can reach `wardlockd:9740` (that is how a Prometheus container would scrape it). Keep untrusted containers off that network, or give Wardlock and its monitoring a network of their own.

</details>

### Logs

Logs are structured (`WARDLOCK_LOG_FORMAT=json` by default, or `text`) on standard error, one line per event, at `WARDLOCK_LOG_LEVEL` (`info` by default). They never carry tokens, cookies, passwords, codes or `Authorization` headers. Keep logs as private as the data directory.

<details>
<summary>What is logged and redacted</summary>

Each request on the main listeners logs `request` with `method`, `host`, `path`, `status` and `duration`. The `path` keeps its query string with credential parameters (`t`, `token`, `code`, `state`, `password`) replaced by `[redacted]`, and any Wardlock token anywhere in the path or query reduced to its public ID (`wl_<public id>_[redacted]`). Events about resources carry IDs (`site_id`, `owner_id`, `policy_id`, `public_id`, `token_id`, `session_id`), never tokens, cookies, passwords, codes or `Authorization` headers; secrets in configuration print as `[redacted]`, SMTP URLs without their credentials. The exceptions are deliberate: the one-time `setup_url` while no owner exists, and with `WARDLOCK_SMTP_URL=dev://` the recipient and subject of each message (never the body or code). No other line names an email address; invitations, for example, log the new owner's ID and the inviter's. Certificate events from certmagic (automatic TLS) keep only reviewed fields, such as names, ACME URLs, states, times and a shortened, scrubbed `error`; any other field is dropped and named in `dropped`, so even at `debug` they carry no keys, challenge values, headers or the ACME contact address ([SECURITY-MODEL, Third-party log output](SECURITY-MODEL.md#third-party-log-output)).

</details>

## Backups

Everything lives in `WARDLOCK_DATA_DIR`: `wardlock.db` and `blobs/` (or `WARDLOCK_BLOB_DIR` if set, or the bucket with the S3 backend; and `certs/`, which can be re-issued). Back up the database with `wardlockd backup` (never copy the live file), then the blobs, and keep `WARDLOCK_SECRET` safe separately.

```sh
# systemd: as the service user, with the service's environment, into a
# directory that user can write (mode 0700)
sudo systemd-run --pipe --wait -p User=wardlock -p EnvironmentFile=/etc/wardlock/wardlock.env \
  -p Environment=WARDLOCK_DATA_DIR=/var/lib/wardlock \
  /usr/local/bin/wardlockd backup /var/backups/wardlock/wardlock-$(date +%F).db
sudo rsync -a /var/lib/wardlock/blobs/ /var/backups/wardlock/blobs/
# Docker (the example compose file): inside the container's volume, then copy out
docker compose -f deploy/compose.yaml exec wardlockd wardlockd backup /data/backup.db
docker compose -f deploy/compose.yaml cp wardlockd:/data/backup.db .
```

<details>
<summary>Database: how <code>wardlockd backup</code> works, and filesystems it cannot write to</summary>

Never copy `wardlock.db` (or its `-wal` and `-shm` files) while the server runs. Use `wardlockd backup /path/to/backup.db` with the same environment as the server (it reads `WARDLOCK_DATA_DIR`). It uses SQLite's online backup API to copy the whole database inside one read transaction, so the copy is the database as of one moment while the server keeps reading and writing (WAL mode does not block writers meanwhile). The copy is written to a temporary file beside the destination, checked with `PRAGMA quick_check`, made a single self-contained file (no `-wal` beside it), synced and moved into place with mode 0600. The destination must not exist yet: the command never overwrites a file. Moving into place is a hard link (which fails rather than replace a file that appeared meanwhile), so the destination's filesystem must support hard links: local filesystems do, but some network shares (SMB/CIFS), FAT/exFAT and some FUSE mounts do not, and the command then fails with `move backup into place`. In that case write the backup to a local directory and copy it to the share afterwards. A failed backup removes its temporary file and any `-journal`, `-wal` or `-shm` beside it. It needs no `WARDLOCK_SECRET`. As an option, [Litestream](https://litestream.io) can replicate the database continuously to object storage.

</details>

<details>
<summary>Blobs on disk and in a bucket</summary>

- **Blobs:** content-addressed and write-once, so a plain `rsync` (after the database backup) is consistent: every blob the backed-up database references already exists, provided garbage collection does not run in between. Schedule backups away from collections, or set `WARDLOCK_GC_INTERVAL_MINUTES=0` and run `wardlockd gc` after each backup.
- **Blobs in a bucket:** `wardlockd backup` copies only the database, never the bucket. Protect the bucket with the service's own tools: turn on versioning, with a lifecycle rule that expires noncurrent versions no sooner than your oldest database backup, so a blob that garbage collection deleted after a backup can be brought back; or replicate the bucket to another bucket or region. A copy with `rclone sync` or `aws s3 sync` taken after each database backup works too, under the same rule about garbage collection as `rsync`.

</details>

### Restore

1. Stop the server.
2. Put the backup in an empty data directory as `wardlock.db`, and the blobs under it as `blobs/` (or at `WARDLOCK_BLOB_DIR`, or in the bucket: restore the previous versions of any blob objects deleted since the backup, or point `WARDLOCK_S3_BUCKET` at the replica). Leave out any `wardlock.db-wal` or `wardlock.db-shm` from elsewhere: they belong to another database. Make both owned by the service user (`chown -R wardlock:wardlock`, mode 0700 for the directory).
3. Start the server with the same `WARDLOCK_SECRET` and hosts. Migrations run automatically, so a backup from an older release is upgraded; a server older than the backup refuses to start (`database is newer than this server`).
4. Check `/readyz` on the operator listener and open a site. Owners, sites, policies, share links, API tokens and sessions written before the backup are back; anything written after it is lost.

Keep `WARDLOCK_SECRET` with your backups, separately and securely. Without it, every share link, API token and session stops working after a restore.

<details>
<summary>How restore is tested</summary>

The server's tests (`TestBackupAndRestore` in `apps/server/cmd/wardlockd`) do exactly this against a running server while it takes writes, and check that the restored instance serves the site, keeps the owner's session and takes new writes.

</details>

## Upgrades

Replace the binary or image and restart. Migrations are forward-only and run on start. Back up first; downgrading after a migration needs the backup.

## Go import paths

The Go modules use vanity import paths on the project's own domain: `wardlock.nz/server`, `wardlock.nz/cli` and `wardlock.nz/apiclient`. This lets the repository move without breaking imports. Until the domain serves `go-import` metadata, local builds rely on `go.work` and the `replace` directives in `apps/server/go.mod` and `apps/cli/go.mod`; serving it is a launch task in the [launch checklist](LAUNCH-CHECKLIST.md).

<details>
<summary>The <code>go-import</code> metadata each path must serve</summary>

For `go get` / `go install` to work, `https://wardlock.nz/<module>?go-get=1` must return HTML with a `go-import` meta tag mapping the module path to the repository and its subdirectory:

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

The fourth field is the module's directory inside the repository; Go supports it from 1.25.

</details>

## Releases and module tags

A release has one version, `vX.Y.Z`, and four tags on the same release commit:

| Tag                            | Used by                                              |
| ------------------------------ | ---------------------------------------------------- |
| `vX.Y.Z`                       | GoReleaser (archives, image, GitHub release)         |
| `packages/apiclient-go/vX.Y.Z` | `go get wardlock.nz/apiclient@vX.Y.Z`                |
| `apps/cli/vX.Y.Z`              | `go install wardlock.nz/cli/cmd/wardlock@vX.Y.Z`     |
| `apps/server/vX.Y.Z`           | `go install wardlock.nz/server/cmd/wardlockd@vX.Y.Z` |

Pushing a `vX.Y.Z` tag is the release decision and needs explicit authorisation. Pull requests, main and manual runs never publish, and the GitHub release is always a draft.

<details>
<summary>Release steps</summary>

Module tags are prefixed with the module's directory, the fourth `go-import` field; a plain `vX.Y.Z` tag is not a version of any module. `go install pkg@version` refuses a module whose `go.mod` has `replace` directives, so the tagged commit cannot be the development state. The release steps are:

1. On the release branch, tag `packages/apiclient-go/vX.Y.Z` at the commit to release.
2. In `apps/cli/go.mod` and `apps/server/go.mod`, drop the `replace wardlock.nz/apiclient` directives, require `wardlock.nz/apiclient vX.Y.Z` where it is used, and run `GOWORK=off go mod tidy` so `go.sum` records the client's hash (the release owner holds the lockfile gate for this). Commit.
3. Tag that commit `apps/cli/vX.Y.Z` and `apps/server/vX.Y.Z` and push those tags (with `packages/apiclient-go/vX.Y.Z`). Module tags never start the publish job.
4. Tag the same commit `vX.Y.Z` and push that tag on its own (`git push origin vX.Y.Z`). GitHub creates no push events when more than three tags are pushed at once, so pushing every tag together can skip the release run. The tag push starts the release workflow: `snapshot` and `module-tags` run first, then `publish` logs in to ghcr.io with the workflow's `GITHUB_TOKEN`, pushes `ghcr.io/cubiecode/wardlock:X.Y.Z` and `:latest`, and creates a draft GitHub release with the archives, SBOMs and `checksums.txt`.
5. Check the draft release, add the release notes and publish it by hand. After the first image push, set the ghcr.io package's visibility.

Main keeps its `replace` directives; only the release commit drops them.

</details>

<details>
<summary>How the tag scheme and the release workflow are tested</summary>

[`scripts/test-module-tags.sh`](../scripts/test-module-tags.sh) proves this scheme without publishing: it makes the release commit and tags in a temporary clone, serves that clone with `git daemon` and answers `wardlock.nz/<module>?go-get=1` from a local proxy, then runs `go install` for both commands. It also checks that an unprefixed tag and a tag on a commit with `replace` directives fail. The tags exist only in the deleted clone. The release workflow runs it on every pull request.

[`.goreleaser.yaml`](../.goreleaser.yaml) builds every artefact, and [`.github/workflows/release.yml`](../.github/workflows/release.yml) runs `goreleaser release --snapshot --clean` on pull requests and main, then [`scripts/release-smoke.sh`](../scripts/release-smoke.sh) checks the snapshot: all 12 archives and SBOMs match `checksums.txt`, `install.sh` installs them and rejects a bad checksum, and the image runs as uid 65532 and answers `/healthz`. Pull requests, main and manual runs never publish: snapshots never push, the image and GitHub release are skipped outside snapshots unless `WARDLOCK_PUBLISH=true`, and the `publish` job that sets it runs only when `github.event_name == 'push'` and the ref starts with `refs/tags/v` in `CubieCode/wardlock`. The workflow triggers on tags matching `v*`; GitHub matches tag filters against the whole tag name and `*` does not match `/`, so `apps/cli/vX.Y.Z`, `apps/server/vX.Y.Z` and `packages/apiclient-go/vX.Y.Z` do not start it. Only `publish` gets `contents: write` and `packages: write`; every other job has `contents: read`. GoReleaser is told the triggering tag (`GORELEASER_CURRENT_TAG`), since the release commit also carries the module tags. The GitHub release is always a draft.

</details>

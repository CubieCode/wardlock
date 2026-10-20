# Wardlock on a VPS

`wardlockd` alone on one VPS with a public IP address: automatic TLS with a Cloudflare DNS-01 wildcard, deploy files in Cloudflare R2, and a nightly restic backup to a second R2 bucket. The walkthrough is in [docs/DEPLOYMENT.md, "VPS with automatic TLS and R2"](../../docs/DEPLOYMENT.md#vps-with-automatic-tls-and-r2-o412).

- `compose.yaml`: the service. Reads `wardlock.env` and, for a source build, `WARDLOCK_IMAGE` from `.env`.
- `wardlock.env.example`: copy to `wardlock.env` (mode 0600) and replace the placeholders.
- `backup.sh`, `wardlock-backup.service`, `wardlock-backup.timer`: the nightly backup.
- `restic.env.example`: copy to `/etc/wardlock/restic.env` (mode 0600).

`wardlock.env` and `restic.env` are ignored by Git; never commit them.

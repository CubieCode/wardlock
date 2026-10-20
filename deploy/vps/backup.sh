#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Nightly backup for the VPS deployment (docs/DEPLOYMENT.md, "Backups and
# restore on the VPS"). Run as root from wardlock-backup.timer or cron:
#
# 1. `wardlockd backup` writes a consistent copy of the database into the
#    data volume, while the server keeps running.
# 2. restic uploads the data volume (that copy plus certs/) to the repository
#    in RESTIC_ENV, leaving out the live wardlock.db and its -wal and -shm
#    files, which cannot be copied safely while the server runs.
# 3. The copy is deleted and old snapshots are pruned.
#
# WARDLOCK_SECRET (wardlock.env) and the restic password are never in the
# volume, so never in the backup. Deploy files are in R2, not in the volume.
set -euo pipefail

compose_file="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/compose.yaml"
restic_env="${RESTIC_ENV:-/etc/wardlock/restic.env}"
volume="${WARDLOCK_VOLUME:-wardlock-data}"

set -a
# shellcheck source=/dev/null
. "$restic_env"
set +a

data="$(docker volume inspect --format '{{ .Mountpoint }}' "$volume")"
name="wardlock-$(date -u +%Y%m%dT%H%M%SZ).db"
trap 'rm -f "$data/$name"' EXIT

docker compose -f "$compose_file" exec -T wardlockd wardlockd backup "/data/$name"
restic backup --quiet --host wardlock --tag wardlock \
  --exclude "$data/wardlock.db" \
  --exclude "$data/wardlock.db-wal" \
  --exclude "$data/wardlock.db-shm" \
  --exclude "$data/blob-cache" \
  "$data"
restic forget --quiet --host wardlock --tag wardlock --prune \
  --keep-daily 14 --keep-weekly 8 --keep-monthly 12
echo "Backed up $volume to $RESTIC_REPOSITORY as of $name."

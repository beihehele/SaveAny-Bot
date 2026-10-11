---
title: "Operations and rollback"
weight: 3
---

This template targets Linux Docker Compose. Use the candidate version's Compose file and configuration, and pin `SAVEANY_IMAGE` to a verified version or digest. Select the deployment directory explicitly. Do not run WSL and the server simultaneously with the same Bot/Userbot account.

## Start, stop, and inspect

```sh
set -eu
DEPLOY_DIR=/srv/saveany-bot
cd "$DEPLOY_DIR"
docker compose up -d
docker compose ps
docker compose exec -T saveany-bot /app/saveany-bot version
docker compose logs --tail=100 saveany-bot
docker compose stop --timeout 30 saveany-bot
```

`up -d` can create or recreate containers; `start` only starts existing containers and does not update images. Do not publish complete configuration, environment variables, sessions, or unredacted logs.

Compose's `restart: unless-stopped` handles recovery when Docker starts. On Linux servers, also check `systemctl is-enabled docker` and `systemctl is-active docker`, and enable Docker according to the server's management policy. Starting a WSL distribution or handling Windows login is a separate concern; this template installs no Windows login task or keepalive process. Manually stopped containers do not resume through this restart policy.

After starting, verify the running commit, container health, console login, mounts, and queue. Check saves and copy/watch using content that may be cleaned up. Container health and Telegram initialization flags do not prove current connectivity or successful delivery.

## Backup while stopped

This example covers only the repository Compose's default persistence layout. For custom paths or volumes, first inventory all persistent paths and adjust the archive list. Do not assume every database, session, or downloaded file is under the default `data` directory.

```sh
set -eu
DEPLOY_DIR=/srv/saveany-bot
BACKUP_DIR=/srv/saveany-backups
cd "$DEPLOY_DIR"
docker compose stop --timeout 30 saveany-bot
SERVICE_ID=$(docker compose ps --all --quiet saveany-bot)
test -n "$SERVICE_ID"
test "$(docker inspect --format '{{.State.Running}}' "$SERVICE_ID")" = false
umask 077
mkdir -p "$BACKUP_DIR"
BACKUP_FILE=$(mktemp "$BACKUP_DIR/saveany-XXXXXXXX.tar.gz")
tar -czf "$BACKUP_FILE" -- config.toml docker-compose.yml .env data downloads cache
sha256sum "$BACKUP_FILE" > "$BACKUP_FILE.sha256"
sha256sum -c "$BACKUP_FILE.sha256"
```

The example assumes `.env` stores the pinned image. If it is not used, remove it from the archive list and separately record the actual image digest. A missing path or a `tar` error means the backup failed: do not upgrade. Ensure other instances and processes have stopped writing; copy SQLite WAL/SHM with their complete directories. Keep backups outside the deployment directory with restricted permissions. Backups contain sessions and secrets and must not be published with source, images, or acceptance reports.

## Upgrade and rollback

1. Retain a readable complete backup, old image digest, and the old version's Compose/configuration.
2. Set `.env`'s `SAVEANY_IMAGE` to the pinned candidate, run `docker compose pull` and `docker compose up -d`, then verify the commit and functionality. Restarting alone does not update the image.
3. On failure, stop the candidate and every instance sharing the account. Verify the old backup checksum, extract into a separate recovery directory, check paths and permissions, and restore the old configuration, complete data, and image. Do not overwrite a running deployment or substitute the candidate's migrated database for the original backup.
4. Verify version, console, preferences, watch routes/topics, sessions, and saved files after rollback, then exercise a limited test fixture. Retain original backups and failure evidence until recovery passes.

Stop WSL before enabling the server; stop the server before rolling back to WSL. Record the deployed commit, image digest, backup location, results, and rollback version. A target server remains a candidate until restoration and functional acceptance pass.

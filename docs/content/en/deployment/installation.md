---
title: "Installation and Updates"
---

# Installation and Updates

## Deploy from Pre-compiled Binary (Recommended)

Use binaries from [this repository's releases](https://github.com/beihehele/SaveAny-Bot/releases), or build a verified commit from this branch. Upstream releases do not include all changes in this fork.

Create a `config.toml` file in the extracted directory, refer to the [Configuration Guide](../configuration) to edit the configuration file.

Run:

```bash
chmod +x saveany-bot
./saveany-bot
```

The release workflows publish Linux/amd64 and Windows/amd64 binaries and Linux/amd64 containers. Other platforms require a separate build and acceptance; obsolete OpenWrt service scripts have been removed.

### Daemon

{{< tabs "daemon" >}}
{{< tab "systemd (Regular Linux)" >}}

Create a file <code>/etc/systemd/system/saveany-bot.service</code> and write the following content:

{{< codeblock >}}
[Unit]
Description=SaveAnyBot
After=systemd-user-sessions.service

[Service]
Type=simple
WorkingDirectory=/yourpath/
ExecStart=/yourpath/saveany-bot
Restart=always

[Install]
WantedBy=multi-user.target
{{< /codeblock >}}

Enable startup on boot and start the service:

{{< codeblock >}}
systemctl enable --now saveany-bot
{{< /codeblock >}}

{{< /tab >}}

{{< /tabs >}}


## Deploy Using Docker

### Build an image from source

Run `docker build -t saveany-bot:local .` from the repository root. The default uses the official Go module proxy. If it is unreachable, select an accessible proxy explicitly, for example:

```bash
docker build --build-arg GOPROXY=https://goproxy.cn,direct -t saveany-bot:local .
```

This build argument controls dependency downloads and does not change the runtime Telegram proxy.

For unpublished changes in the current checkout, use `docker compose -f docker-compose.local.yml up -d --build`; it builds the local source without `SAVEANY_IMAGE`.

### Docker Compose

Check out the release tag/commit matching the target image and use its `docker-compose.yml` and `config.example.toml`, saving your configuration as `config.toml`. Do not mix examples from `main`. Set `SAVEANY_IMAGE` to a verified full image tag or digest, directly or in a local `.env` file. Compose rejects a missing value. Replace `YOUR_VERIFIED_VERSION` below with the selected version.

```bash
export SAVEANY_IMAGE='ghcr.io/beihehele/saveany-bot:YOUR_VERIFIED_VERSION'
```

Start:

```bash
docker compose up -d
```

### Docker

```shell
docker run -d --name saveany-bot --restart unless-stopped \
    -v /path/to/config.toml:/app/config.toml \
    -v /path/to/data:/app/data \
    -v /path/to/cache:/app/cache \
    -v /path/to/downloads:/app/downloads \
    "${SAVEANY_IMAGE:?Set SAVEANY_IMAGE to a verified tag or digest}"
```

{{< hint info >}}
This branch image contains the program and its configuration fetch tool. Pin a verified image version or digest and validate it in isolation.
{{< /hint >}}

`data` stores the database and Telegram sessions and must survive container recreation. Use a dedicated `cache` directory for temporary files; do not share it with `data` or `downloads`.

The program no longer clears entire cache directories on exit. Each task cleans files it created; forced or timed-out shutdown can leave leftovers. Inspect directory purpose and file contents while stopped before manual cleanup. The legacy `no_clean_cache` setting and `--no-clean-cache` flag have been removed; delete them before upgrading.

When the container environment variable `CONFIG_URL` is set to an HTTP(S) URL, the application loads that configuration directly with a 30-second download timeout. HTTP errors, incomplete responses, or invalid configuration prevent startup. Remote configuration does not overwrite `/app/config.toml` or a host-mounted file and is not persisted locally; without this variable, the application continues to load local configuration.

## Updates

Before upgrading, stop the service and copy the configuration, the entire `data` directory and private deployment files to a separate backup directory, including database and Bot/UserBot sessions. Retain the old binary or image digest. Do not delete the database to bypass migration failures. Roll back while stopped using a verified complete backup and the old program; a migrated database is not guaranteed to work with an older version. Removing users from configuration still invokes the existing user synchronization and associated-data deletion, so check the user list first.

For binary deployments, back up configuration and data, stop the service, replace the binary with a verified release, and restart. Read the migration guide first and retain the previous binary and data backup.

This version has no Bot or CLI self-update. Replace binaries or Docker images manually with a verified version.

If you deployed with Docker, use the following commands to update:

Before updating Docker deployments, verify that `data` is persistent and backed up. If the old container has no `/app/data` mount, stop it and export that directory with `docker cp saveany-bot:/app/data /path/to/data-backup`, then mount this exported data at `/app/data` in the replacement container. Preserve your existing proxy and other applicable runtime options.

For deployments using the mounts shown above:

```bash
docker pull "${SAVEANY_IMAGE:?Set SAVEANY_IMAGE to a verified tag or digest}"
docker stop saveany-bot
docker rm saveany-bot
docker run -d --name saveany-bot --restart unless-stopped \
    -v /path/to/config.toml:/app/config.toml \
    -v /path/to/data:/app/data \
    -v /path/to/cache:/app/cache \
    -v /path/to/downloads:/app/downloads \
    "${SAVEANY_IMAGE:?Set SAVEANY_IMAGE to a verified tag or digest}"
```

docker compose:

```bash
docker compose pull
docker compose up -d
```

Restarting an existing container does not select the newly pulled image. Compose `up -d` recreates containers when their image changes and preserves mounted data; see the [Docker reference](https://docs.docker.com/reference/cli/docker/compose/up/). Set `SAVEANY_IMAGE` to the verified version or digest for this update and check its matching configuration first.

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

{{< tab "procd (OpenWrt)" >}}

<h4>Add Boot Autostart Service</h4>

Create a file <code>/etc/init.d/saveanybot</code>, refer to <a href="https://github.com/krau/SaveAny-Bot/blob/main/docs/confs/wrt_init" target="_blank">wrt_init</a> and modify as needed:

{{< codeblock >}}
#!/bin/sh /etc/rc.common

#This is the OpenWRT init.d script for SaveAnyBot

START=99 
STOP=10
description="SaveAnyBot"

WORKING_DIR="/mnt/mmc1-1/SaveAnyBot"
EXEC_PATH="$WORKING_DIR/saveany-bot"
start() {
    echo "Starting SaveAnyBot..."
    cd $WORKING_DIR
    $EXEC_PATH &
}
stop() {
    echo "Stopping SaveAnyBot..."
    killall saveany-bot
}
reload() {
    stop
    start
}

{{< /codeblock >}}

Set permissions:

{{< codeblock >}}
chmod +x /etc/init.d/saveanybot
{{< /codeblock >}}

Then copy the file to <code>/etc/rc.d</code> and rename it to <code>S99saveanybot</code>, also set permissions:

{{< codeblock >}}
chmod +x /etc/rc.d/S99saveanybot
{{< /codeblock >}}

<h4>Add Shortcut Commands</h4>

Create a file <code>/usr/bin/sabot</code>, refer to <a href="https://github.com/krau/SaveAny-Bot/blob/main/docs/confs/wrt_bin" target="_blank">wrt_bin</a> and modify as needed. Note that the file encoding here only supports ANSI 936.

Then set permissions:

{{< codeblock >}}
chmod +x /usr/bin/sabot
{{< /codeblock >}}

Usage: <code>sudo sabot start|stop|restart|status|enable|disable</code>

{{< /tab >}}
{{< /tabs >}}


## Deploy Using Docker

### Docker Compose

Download the [docker-compose.yml](https://github.com/beihehele/SaveAny-Bot/blob/main/docker-compose.yml) file, create a new `config.toml` file in the same directory, refer to [config.example.toml](https://github.com/beihehele/SaveAny-Bot/blob/main/config.example.toml) to edit the configuration file.

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
    ghcr.io/beihehele/saveany-bot:latest
```

{{< hint info >}}
The default image includes the full program, FFmpeg, and yt-dlp. Rclone requires separate installation and configuration; Playwright plugins need a compatible browser environment. Pin stable deployments to a verified image version or digest and validate the features you use in an isolated environment.
{{< /hint >}}

`data` stores the database and Telegram sessions and must survive container recreation. Use a dedicated `cache` directory for temporary files; do not share it with `data` or `downloads`.

The program no longer clears entire cache directories on exit. Each task cleans files it created; forced or timed-out shutdown can leave leftovers. Inspect directory purpose and file contents while stopped before manual cleanup. The legacy `no_clean_cache` option remains accepted but no longer controls recursive cleanup.

When the container environment variable `CONFIG_URL` is set to an HTTP(S) URL, the application loads that configuration directly with a 30-second download timeout. HTTP errors, incomplete responses, or invalid configuration prevent startup. Remote configuration does not overwrite `/app/config.toml` or a host-mounted file and is not persisted locally; without this variable, the application continues to load local configuration.

## Updates

Before upgrading, stop the service and copy the configuration, the entire `data` directory and custom plugins to a separate backup directory, including database and Bot/UserBot sessions. Retain the old binary or image digest. Do not delete the database to bypass migration failures. Roll back while stopped using a verified complete backup and the old program; a migrated database is not guaranteed to work with an older version. Removing users from configuration still invokes the existing user synchronization and associated-data deletion, so check the user list first.

For binary deployments, back up the configuration and `data`, stop the service, replace the binary with a verified release from this repository, then restart it. `up` / `upgrade` and in-bot `/update` check releases from `beihehele/SaveAny-Bot`. Development builds cannot self-update. Prefer a stopped-service update until in-bot update shutdown and recovery have been validated.

Self-update accepts newer stable releases within the same major version and uses default-build assets from this repository's workflow. Docker and builds with tags, including micro/pico, require manual updates. Release checks have a 30-second deadline; download and preparation have a 5-minute deadline. Assets and total decompressed tar data are each limited to 512 MiB; executable extraction has the same limit. Downloads use the selected asset ID and check size, SHA-256, program module and platform build information. Missing both the GitHub asset digest and a release `.sha256` file prevents self-update. CLI failures return a nonzero exit code; expired Bot buttons or changed assets require another `/update`.

Cancellation is checked before commit; once commit starts, replacement or rollback finishes. Successful replacement retains the previous binary as `.saveany-update-old-*` in the same directory and reports its path. This backup excludes configuration, database and sessions. Stop the service and check the complete backup before recovery; remove the corresponding old binary manually after validating the new version. SHA-256 checks still trust this repository's publisher; independent signature verification is not implemented.

If you deployed with Docker, use the following commands to update:

Before updating Docker deployments, verify that `data` is persistent and backed up. If the old container has no `/app/data` mount, stop it and export that directory with `docker cp saveany-bot:/app/data /path/to/data-backup`, then mount this exported data at `/app/data` in the replacement container. Preserve your existing plugin, proxy, and other runtime options.

For deployments using the mounts shown above:

```bash
docker pull ghcr.io/beihehele/saveany-bot:latest
docker stop saveany-bot
docker rm saveany-bot
docker run -d --name saveany-bot --restart unless-stopped \
    -v /path/to/config.toml:/app/config.toml \
    -v /path/to/data:/app/data \
    -v /path/to/cache:/app/cache \
    -v /path/to/downloads:/app/downloads \
    ghcr.io/beihehele/saveany-bot:latest
```

docker compose:

```bash
docker compose pull
docker compose up -d
```

Restarting an existing container does not select the newly pulled image. Compose `up -d` recreates containers when their image changes and preserves mounted data; see the [Docker reference](https://docs.docker.com/reference/cli/docker/compose/up/). Replace `latest` in these examples with the version or digest verified for this update.

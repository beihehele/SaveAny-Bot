<div align="center">

# <img src="docs/static/logo.png" width="45" align="center"> Save Any Bot

**English** | [简体中文](./README_zh.md)

> **Save Telegram files locally and manage chat watches and history copies.**

[![Release Date](https://img.shields.io/github/release-date/beihehele/SaveAny-Bot?label=release)](https://github.com/beihehele/SaveAny-Bot/releases)
[![tag](https://img.shields.io/github/v/tag/beihehele/SaveAny-Bot.svg)](https://github.com/beihehele/SaveAny-Bot/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/beihehele/SaveAny-Bot/build-release.yml)](https://github.com/beihehele/SaveAny-Bot/actions/workflows/build-release.yml)
[![Stars](https://img.shields.io/github/stars/beihehele/SaveAny-Bot?style=flat)](https://github.com/beihehele/SaveAny-Bot/stargazers)
[![Downloads](https://img.shields.io/github/downloads/beihehele/SaveAny-Bot/total)](https://github.com/beihehele/SaveAny-Bot/releases)
[![Issues](https://img.shields.io/github/issues/beihehele/SaveAny-Bot)](https://github.com/beihehele/SaveAny-Bot/issues)
[![Pull Requests](https://img.shields.io/github/issues-pr/beihehele/SaveAny-Bot?label=pr)](https://github.com/beihehele/SaveAny-Bot/pulls)
[![License](https://img.shields.io/github/license/beihehele/SaveAny-Bot)](./LICENSE)

</div>

## Features

- Save Telegram documents, photos, videos and albums to local directories.
- Watch chats and copy history, retaining filters, album boundaries and topics.
- Organize local saves with directory rules and filename templates.
- Inspect and cancel tasks through Telegram or the optional admin console.
- Keep multiple named Local roots and per-user storage access settings.

## 📦 Quick Start

Create a `config.toml` file with the following content:

```toml
lang = "en" # Language setting, "en" for English
[telegram]
token = "" # Your bot token, obtained from @BotFather
[telegram.proxy]
# Enable proxy for Telegram
enable = false
url = "socks5://127.0.0.1:7890"

[[storages]]
name = "Local Disk"
type = "local"
enable = true
base_path = "./downloads"

[[users]]
id = 114514 # Your Telegram account id
storages = []
blacklist = true
```

To run the current checkout, build its image first:

```bash
docker build -t saveany-bot:local .
```

Run it with Docker:

```bash
docker run -d --name saveany-bot --restart unless-stopped \
    -v ./config.toml:/app/config.toml \
    -v ./data:/app/data \
    -v ./cache:/app/cache \
    -v ./downloads:/app/downloads \
    saveany-bot:local
```

The `data` directory contains the database and Telegram sessions. Keep it when recreating the container; use `cache` only for temporary files. Pin an image version or digest that you have verified for stable deployments.


For prebuilt images, select a verified tag or digest and use configuration/examples from its matching release tag or commit. The remote Compose example requires `SAVEANY_IMAGE`; `latest` may refer to a different product revision. Unpublished local changes require a local build.

Read this fork's [installation and update guide](docs/content/en/deployment/installation.md) and [configuration guide](docs/content/en/deployment/configuration/_index.md). The [upstream documentation site](https://sabot.unv.app/en/) may describe a different revision.

## Development and tests


```bash
go test ./...
go vet ./...
```

Use a separate Bot, UserBot session, and test chats for live Telegram testing, following the [media group acceptance checklist](docs/content/en/usage/watch.md#media-group-acceptance-checklist). Record automated test results and live acceptance results separately.

## Sponsors

This project is supported by [YxVM](https://yxvm.com/) and [NodeSupport](https://github.com/NodeSeekDev/NodeSupport).

If this project is helpful to you, consider sponsoring me via:

- [Afdian](https://afdian.com/a/unvapp)

## Thanks To

- [gotd](https://github.com/gotd/td)
- [TG-FileStreamBot](https://github.com/EverythingSuckz/TG-FileStreamBot)
- [gotgproto](https://github.com/celestix/gotgproto)
- [tdl](https://github.com/iyear/tdl)
- All the dependencies, contributors, sponsors and users.

## Contact

- [![Group](https://img.shields.io/badge/ProjectSaveAny-Group-blue)](https://t.me/ProjectSaveAny)
- [![Discussion](https://img.shields.io/badge/Github-Discussion-white)](https://github.com/beihehele/SaveAny-Bot/discussions)
- [![PersonalChannel](https://img.shields.io/badge/Krau-PersonalChannel-cyan)](https://t.me/acherkrau)

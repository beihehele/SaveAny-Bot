<div align="center">

# <img src="docs/static/logo.png" width="45" align="center"> Save Any Bot

**English** | [简体中文](./README_zh.md)

> **Save Any Telegram File to Anywhere 📂. Support restrict saving content and beyond telegram.**

[![Release Date](https://img.shields.io/github/release-date/beihehele/SaveAny-Bot?label=release)](https://github.com/beihehele/SaveAny-Bot/releases)
[![tag](https://img.shields.io/github/v/tag/beihehele/SaveAny-Bot.svg)](https://github.com/beihehele/SaveAny-Bot/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/beihehele/SaveAny-Bot/build-release.yml)](https://github.com/beihehele/SaveAny-Bot/actions/workflows/build-release.yml)
[![Stars](https://img.shields.io/github/stars/beihehele/SaveAny-Bot?style=flat)](https://github.com/beihehele/SaveAny-Bot/stargazers)
[![Downloads](https://img.shields.io/github/downloads/beihehele/SaveAny-Bot/total)](https://github.com/beihehele/SaveAny-Bot/releases)
[![Issues](https://img.shields.io/github/issues/beihehele/SaveAny-Bot)](https://github.com/beihehele/SaveAny-Bot/issues)
[![Pull Requests](https://img.shields.io/github/issues-pr/beihehele/SaveAny-Bot?label=pr)](https://github.com/beihehele/SaveAny-Bot/pulls)
[![License](https://img.shields.io/github/license/beihehele/SaveAny-Bot)](./LICENSE)

</div>

## 🎯 Features

- Support documents / videos / photos / stickers… and even [Telegraph](https://telegra.ph/)
- Bypass "restrict saving content" media
- Batch download
- Streaming transfer
- Multi-user support
- Auto organize files based on storage rules
- Watch specified chats and auto-save messages, with filters
- Transfer files between different storage backends
- Integrate with yt-dlp to download and save media from 1000+ websites
- Aria2 integration to download files from URLs/magnets and save to storages
- Write JS parser plugins to save files from almost any website
- Storage backends:
  - Alist
  - S3
  - WebDAV
  - Local filesystem
  - Rclone (via command line)
  - Telegram (re-upload to specified chats)

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

Run Save Any Bot with Docker:

```bash
docker run -d --name saveany-bot \
    -v ./config.toml:/app/config.toml \
    -v ./downloads:/app/downloads \
    ghcr.io/beihehele/saveany-bot:latest
```

Please [**read the docs**](https://sabot.unv.app/en/) for more configuration options and usage.

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
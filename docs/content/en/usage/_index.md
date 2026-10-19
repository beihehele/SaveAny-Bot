---
title: "Usage"
weight: 10
---

# Usage

Send or forward media to the bot, or send Telegram message links, to save files locally, to ShardReel or to a TG collection. `/save` supports replied media and history ranges. Silent saves use the default storage and directory.

- [Watch chats](watch) and [copy history](copy), including albums and topics.
- [`/collect` shortcut](../deployment/configuration/storages/#tg-collection): collect one URL to the configured or task-local Telegram target.
- [Storage rules](rules), [filename settings](config), and [silent saves](silent).
- [Admin console](admin), [HTTP API](api), and [CLI](cli).

Use `/task` to inspect jobs and `/cancel` to cancel them. Local/ShardReel directories are relative to their storage roots; TG collection does not use directories.

---
title: "Telegram-only migration"
weight: 30
---

# Telegram-only migration

This version retains Telegram watching/history copying, local Telegram saves and management. Stop the instance and back up its binary/image identity, configuration, database, Bot/Userbot sessions and downloads first. No saved files or stale storage references are automatically deleted or redirected.

Only enable `type="local"` roots, retaining existing names and paths. User allowlists, defaults, directories and rules must reference enabled roots; `CHOSEN` rules keep their existing meaning. Remove obsolete Aria2, video/parser and remote backend configuration. Enabled removed features/backends are rejected. Inactive historical Aria2/parser/storage blocks can be ignored during migration; remove them from the final configuration. A `[ytdlp]` section is rejected.

Delete the legacy `no_clean_cache` setting and `--no-clean-cache` flag. Exit still preserves cache directories and lets tasks clean up their own temporary files. Stale database references are reported, never silently moved to another root. Correct them in a verified backup copy or retain matching Local names before switching versions.

Removed Bot commands: `/dl`, `/aria2dl`, `/ytdlp`, `/parser`, `/transfer`, `/update`. Removed CLI commands: `upload`, `watch`, `upgrade`. Telegram `/watch` and `/copy` remain. API creation accepts only `tgfiles`; `path` is a directory for both individual files and albums. Other task types are rejected.

Images no longer contain FFmpeg, ffprobe or yt-dlp. Telegram forwarding and raw local saves do not need them; Telegram re-upload storage is unsupported.

Build and validate the candidate in isolation before manually switching deployment versions. Keep the old binary/image and matching configuration for recovery. Verify stored routes/settings, single/album saves, topics, copying/cancellation, console login/downloads and restart persistence. Real Telegram acceptance requires separate accounts and chats and remains pending without that environment.

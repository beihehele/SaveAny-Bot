---
title: "CLI Subcommands"
weight: 21
---

# CLI

Run `saveany-bot --config config.toml` to start the bot.

- `version` (alias `v`): version, commit and build metadata.
- `admin-password`: generate an admin password hash interactively, or use `--stdin`.
- `help` and `completion`: command help and shell completion.

Global flags cover configuration, language, workers, retries, download threads, streaming, proxies, logging, Telegram, database and temporary paths. Run `saveany-bot --help` for the current list.

Telegram `/watch` manages chat watching through the bot. The Docker entrypoint starts the bot without forwarding CLI arguments; use `--entrypoint /app/saveany-bot` for other commands.

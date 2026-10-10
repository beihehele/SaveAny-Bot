---
title: "Configuration Guide"
---

# Configuration

Prepare a UTF-8 TOML file before startup. The default is `config.toml` in the working directory; `--config` accepts a file or HTTP(S) URL. Missing configuration is an error, not an automatic file creation. Environment overrides use `SAVEANY_` and underscores between levels.

Use this repository's `config.example.toml` or `config.example.full.toml`. Configure language (`zh-Hans` or `en`), workers, download threads, retries, streaming, Telegram credentials and proxy, Userbot session, database/cache paths, Local roots, authorized users, hooks, and optional API/admin listeners.

Persist the database and sessions. Keep download roots separate from configuration and session files. `/storage` chooses the default root; local watch saves require a usable default root.

See [local storage](storages), [admin](../../usage/admin), [API](../../usage/api), and the [migration guide](../../usage/migration).

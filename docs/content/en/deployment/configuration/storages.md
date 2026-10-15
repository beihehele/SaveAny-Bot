---
title: "Storage Configuration"
---

# Storage backends

`local` and `shardreel` are supported. Keep local roots and optionally add a ShardReel endpoint.

```toml
[[storages]]
name = "archive"
type = "local"
enable = true
base_path = "./downloads"
```

Names must be unique and enabled roots require `base_path`. Mount `/app/downloads` in Docker. User `storages` lists allowed or excluded names according to `blacklist`.

Use `/storage` for the default root, `/dir` for saved directory shortcuts and `/rule` for routing to local roots. Deleting a directory shortcut does not delete saved files. The console can browse and download local files.

## ShardReel

Run the services independently, including on different LAN machines. Enable `SR_STORAGE_API_KEY` (at least 32 bytes), `SR_STORAGE_ROOT=Telegram` and the Worker in ShardReel. Configure the Bot:

```toml
[[storages]]
name = "drive"
type = "shardreel"
enable = true
url = "http://YOUR_SHARDREEL_HOST:8080"
api_key = "YOUR_STORAGE_API_KEY_WITH_AT_LEAST_32_BYTES"
spool_path = "./data/shardreel"
max_pending_bytes = 21474836480
```

The persistent outbox defaults to `data/shardreel/<storage name>`, with a 20 GiB budget. Each store runs one uploader. Complete Telegram downloads release download slots; terminal feedback and hooks wait for verified remote ingest. This backend stages files even when streaming is enabled. Empty files are unsupported by ShardReel; choose local storage for them.

Both Compose files explicitly mount `./shardreel:/app/data/shardreel`. Retain and back up the host `data` and `shardreel` directories together with the service stopped. Adjust the mount when placing a custom `spool_path` elsewhere.

Bot, web/API and watch saves put albums under the earliest usable caption by message ID, relative to the selected base directory. Invalid path characters, controls and whitespace are cleaned; names are limited to 64 characters and 180 UTF-8 bytes without IDs or random suffixes. Equal effective names share a folder within the same storage/base directory, with existing file-conflict policies preserved. Empty captions use `album_<chat ID>_<group ID>`. This applies to local and ShardReel saves, leaving Telegram forwarding unchanged. Temporary download and upload payloads remain isolated by task ID; their target directories are recorded in the database.

Successful files are removed locally. Cleanup failures retry hourly without reuploading. Temporary failures retry after 1m, 5m, 15m, 1h and 3h; permanent errors need attention. The console can retry retained files or discard tasks after confirmed cancellation. Successful album members are not uploaded again.

Restart recovery covers complete staged files; interrupted Telegram downloads must be submitted again. Cancelling a task that never contacted the remote requires no connection; started transfers require confirmed remote cancellation or completion before cleanup. Cancelling an unstarted upload in ShardReel also retains a cancellation receipt, so the Bot will not recreate it. Originals remain byte-identical; media optimization uses ShardReel playback caches. Clean terminal history is retained for seven days, at most 1000 tasks. Pending, failed and unclean tasks are retained. Back up the database and outbox together with the service stopped. Keep the storage name, endpoint, outbox path and ShardReel `SR_STORAGE_ROOT` stable while files are pending.

`/watch <source> 0` follows default storage and enabled rules. `/copy` remains Telegram-to-Telegram forwarding.

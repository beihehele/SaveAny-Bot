---
title: "Storage Configuration"
---

# Storage backends

`local`, `shardreel` and `telegram` are supported. Their configured names appear in Bot and web storage selectors.

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

Successful files are removed locally. Cleanup failures retry hourly without reuploading. Temporary failures retry after 1m, 5m, 15m, 1h and 3h; permanent errors need attention. The task list can cancel active uploads or delete terminal records together with retained retry payloads after confirmed remote cancellation. Successful album members are not uploaded again.

Remote ingest is retried automatically only when `can_retry=true`. Accepted retries remain under observation; temporary status-query failures do not exhaust the ingest retry budget. Skip keeps only an already ingested file; directories and unfinished files report a conflict. The receiver checks the skip policy again at admission to prevent an intervening name conflict from creating a renamed copy. The Bot accepts remote paths up to 2048 UTF-8 bytes, with at most 512 characters per name, no control characters and no leading or trailing whitespace in names.

Restart recovery covers complete staged files; interrupted Telegram downloads must be submitted again. Cancelling a task that never contacted the remote requires no connection; started transfers require confirmed remote cancellation or completion before cleanup. Cancelling an unstarted upload in ShardReel also retains a cancellation receipt, so the Bot will not recreate it. Originals remain byte-identical; media optimization uses ShardReel playback caches. Clean terminal history is retained for seven days, at most 1000 tasks. Pending, failed and unclean tasks are retained. Back up the database and outbox together with the service stopped. Keep the storage name, endpoint, outbox path and ShardReel `SR_STORAGE_ROOT` stable while files are pending.

`/watch <source> 0` follows default storage and enabled rules. `/copy` remains Telegram-to-Telegram forwarding.

## TG collection

```toml
# Requires telegram.userbot.enable and a logged-in Userbot
[[storages]]
name = "TG收藏"
type = "telegram"
enable = true
chat_id = -1001234567890
topic_id = 0
```

Use a writable channel/supergroup and optionally an existing forum topic. This does not change the default storage. Link saves, Bot attachments and default-storage watches (`/watch … 0`) can use it; `/copy` and direct-target watches retain their existing behavior.

Channel/group media reuse DropAuthor copying and best-effort `[转]` source links. Only an explicit forwarding restriction triggers download/upload fallback; network and access errors do not. Bot-private attachments use their original downloader and Userbot upload because their message IDs belong to the Bot dialog. Original forwarded channel links are retained when available; new attachments have no invented source URL. Protected downloads can still fail.

Albums require complete input (no `?single`), retain member order, captions, entities and document attributes, and are published only after all uploads are prepared. Channel/group albums are checked against a fresh bounded source query before sending; incomplete or changed selections fail without adding members that might bypass rules. This query cannot guarantee discovery of members outside its range. Bot-private attachments use only received members. Rules must route all members to one storage. Directories, file conflict policies and browsing do not apply. Fallback uses the existing `temp.base_path` (`/app/cache` in Docker); each cache is removed after upload. Task details expose delivery stages and collection links. Cancellation stops subsequent work without recalling confirmed posts; record deletion leaves posts intact. Check the target before resubmitting after ambiguous network errors. Like ordinary saves, these tasks do not resume across restart.

Collection targets cannot be watch sources. Remove any conflicting route before enabling the storage; startup validation identifies existing conflicts.

### /collect shortcut

- `/collect https://t.me/channel/123`: use the sole available collection storage, including its topic.
- `/collect https://t.me/channel/123 -1001234567890`: override the target for this task, without a topic.
- `/collect https://t.me/channel/123 -1001234567890 2`: target Topic 2; topic 0 means no topic.

Use a numeric chat ID and a separate topic argument. One URL selects the complete message/album without storage/directory prompts. This command bypasses directory/naming/storage rules and does not modify defaults, configuration or watches. A configured and authorized collection storage is still required; multiple available collection configurations are rejected rather than guessing a default.

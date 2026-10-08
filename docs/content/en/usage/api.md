---
title: "HTTP API"
weight: 20
---

# HTTP API

SaveAny-Bot provides an HTTP API that allows you to programmatically create download/transfer tasks, query task status, cancel tasks, and more — without going through Telegram.

## Enabling the API

Add or modify the following section in `config.toml`:

```toml
[api]
enable = true
host   = "0.0.0.0"   # Bind address, default 0.0.0.0
port   = 8080         # Listen port, default 8080
token  = "your-token" # Required authentication token
```

You can also override these settings with environment variables (prefix `SAVEANY_`):

| Environment Variable | Config Key |
|---|---|
| `SAVEANY_API_ENABLE` | `api.enable` |
| `SAVEANY_API_HOST` | `api.host` |
| `SAVEANY_API_PORT` | `api.port` |
| `SAVEANY_API_TOKEN` | `api.token` |

{{< hint warning >}}
An enabled API requires a nonempty `token`. With an empty token the API refuses to start; the Bot can still run, so check startup logs.
{{< /hint >}}

{{< hint warning >}}
API-created tasks are kept in **process memory only**: after a restart the task list and progress are cleared, and in-flight tasks are interrupted. Do not rely on the API for cross-restart orchestration.
{{< /hint >}}

## Authentication

All API requests, including `/health`, must include a Bearer token in the HTTP header:

```
Authorization: Bearer <your-token>
```

On authentication failure, the server returns `401`:

```json
{ "error": "unauthorized", "message": "invalid token" }
```

## Error Response Format

All errors use a consistent JSON format:

```json
{
  "error":   "error_code",
  "message": "human readable description"
}
```

Common error codes:

| Error Code | HTTP Status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Authentication failed |
| `method_not_allowed` | 405 | Wrong HTTP method |
| `invalid_request` | 400 | Malformed request body or parameters |
| `request_too_large` | 413 | Create-task request body exceeds 1 MiB |
| `task_creation_failed` | 400 | Failed to create task |
| `task_not_found` | 404 | Task ID does not exist |
| `cancel_failed` | 500 | Failed to cancel task |
| `internal_error` | 500 | Internal server error |

---

## Endpoints

### GET /health — Health Check

Authentication is required. This checks HTTP liveness only; it does not certify Telegram, external commands or storage readiness.

**Response `200 OK`:**

```json
{ "status": "ok" }
```

---

### GET /api/v1/storages — List Storages

Returns all currently loaded storage backends.

Each entry also includes `readable`, `listable`, `stream` and `detect_existence` booleans describing reading, listing, streaming uploads and existence checks. These interface capabilities do not certify external service connectivity.

**Response `200 OK`:**

```json
{
  "storages": [
    { "name": "local",   "type": "local" },
    { "name": "MyMinio", "type": "s3" }
  ]
}
```

---

### GET /api/v1/task-types — List Supported Task Types

`types` retains the implemented API task types. The additional `capabilities` array supplies `type`, `available` and an optional `reason` for each type. Checks cover local prerequisites such as Aria2 configuration, yt-dlp resolution, registered parsers and Telegram client initialization, without running downloads or probing remote services. Availability does not guarantee success for a particular URL, storage or credential.

**Response `200 OK`:**

```json
{
  "types": [
    "directlinks",
    "ytdlp",
    "aria2",
    "parseditem",
    "tgfiles",
    "tphpics",
    "transfer"
  ]
}
```

---

### POST /api/v1/tasks — Create Task

Bodies are limited to 1 MiB and must contain one JSON document. Preprocessing receives the request context with a 30-second deadline. JS plugin matching, pure JavaScript execution, and `ghttp` honor cancellation. Playwright checks cancellation while waiting for installation and between stages; browser launch and navigation use the shorter remaining deadline. Installation already in progress, driver startup, page operations, cleanup, and some underlying Telegram calls still lack full cancellation support, so this is not a hard deadline for every type. Enqueued tasks use the service context; disconnecting the HTTP client does not cancel them.

**Request headers:**

```
Content-Type: application/json
Authorization: Bearer <token>
```

**Request body:**

```json
{
  "type":    "<task_type>",
  "storage": "<storage_name>",
  "path":    "<subpath>",
  "webhook": "<callback_url>",
  "params":  { }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | string | Yes | Task type — see below |
| `storage` | string | Yes | Target storage name, must match a name in your config |
| `path` | string | No | Subdirectory path within the storage |
| `webhook` | string | No | Callback URL invoked when the task reaches a terminal state |
| `params` | object | Yes | Type-specific parameters — see below |
| `result_policy` | string | No | Supported only by `transfer`; `legacy` keeps existing completion semantics and adds counts, `strict` requires every submitted file to succeed; omission preserves the original contract |

**Response `201 Created`:**

```json
{
  "task_id":    "abc123xyz",
  "type":       "directlinks",
  "status":     "queued",
  "created_at": "2026-03-11T10:00:00Z"
}
```

#### Task Types and params

##### directlinks — Direct URL Download

Download one or more files from direct HTTP/HTTPS URLs.

```json
{
  "type":    "directlinks",
  "storage": "local",
  "path":    "downloads",
  "params": {
    "urls": [
      "https://example.com/file.zip",
      "https://example.com/other.zip"
    ]
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `urls` | []string | Yes | List of download URLs, at least 1 |

##### ytdlp — yt-dlp Media Download

{{< hint warning >}}
Requires yt-dlp to be installed on the system.
{{< /hint >}}

Download videos or audio via yt-dlp, supporting YouTube, Bilibili, and 1000+ other sites.

```json
{
  "type":    "ytdlp",
  "storage": "local",
  "path":    "videos",
  "params": {
    "urls":  ["https://www.youtube.com/watch?v=xxx"],
    "flags": ["--extract-audio", "--audio-format", "mp3"]
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `urls` | []string | Yes | List of media URLs, at least 1 |
| `flags` | []string | No | Extra yt-dlp command-line flags |

##### aria2 — Aria2 Download

{{< hint warning >}}
Requires Aria2 to be enabled and configured (RPC) in the config file.
{{< /hint >}}

Download files via the Aria2 download manager, supporting HTTP/HTTPS, FTP, BitTorrent (magnet links, torrent files), and more.

```json
{
  "type":    "aria2",
  "storage": "local",
  "path":    "downloads",
  "params": {
    "urls":    ["magnet:?xt=urn:btih:..."],
    "options": { "split": "4" }
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `urls` | []string | Yes | List of download URIs, at least 1 |
| `options` | map[string]string | No | Aria2 download options |

##### parseditem — Parser Plugin Download

Hand a URL off to a registered JS plugin or built-in parser for processing and downloading.

```json
{
  "type":    "parseditem",
  "storage": "local",
  "path":    "parsed",
  "params": {
    "url": "https://some-site.com/page"
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `url` | string | Yes | The URL to parse |

Returns `400 task_creation_failed` if no parser is able to handle the URL.

##### tgfiles — Telegram Message File Download

Download files from Telegram messages via message links. Supported link formats:

- `https://t.me/username/123` — public channel or group
- `https://t.me/c/123456789/123` — private channel by numeric ID
- `https://t.me/c/123456789/111/456` — topic message (thread ID / message ID)
- `https://t.me/username/111/456` — topic under a username-based chat

If the message is part of a media group (album), all files in the group are downloaded by default. Append `?single` to the link to force downloading only the single specified message.

```json
{
  "type":    "tgfiles",
  "storage": "local",
  "path":    "telegram",
  "params": {
    "message_links": [
      "https://t.me/username/123",
      "https://t.me/c/1234567890/456"
    ]
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `message_links` | []string | Yes | List of Telegram message links, at least 1 |

##### tphpics — Telegraph Article Images

Download all images from a Telegra.ph article.

Supported URL prefixes: `https://telegra.ph/`, `http://telegra.ph/`, `https://telegraph.co/`, `http://telegraph.co/`

```json
{
  "type":    "tphpics",
  "storage": "local",
  "path":    "telegraph",
  "params": {
    "telegraph_url": "https://telegra.ph/Some-Article-01-01"
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `telegraph_url` | string | Yes | URL of the Telegra.ph article |

##### transfer — Storage-to-Storage Transfer

Transfer files directly between two storage backends without going through Telegram. The source storage must support both listing and reading.

{{< hint info >}}
For `transfer` tasks, the top-level `storage` field is still required for validation, but the actual storages used are determined by `source_storage` and `target_storage` inside `params`.
{{< /hint >}}

```json
{
  "type":    "transfer",
  "storage": "local",
  "params": {
    "source_storage": "MyS3",
    "source_path":    "backups/",
    "target_storage": "LocalDisk",
    "target_path":    "restored/"
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `source_storage` | string | Yes | Source storage name |
| `source_path` | string | Yes | Path within the source storage; must contain at least one file |
| `target_storage` | string | Yes | Target storage name |
| `target_path` | string | Yes | Destination path within the target storage |

###### Transfer result policy

`result_policy` is a top-level, per-request option. It does not change global configuration or other tasks. In `GET /api/v1/task-types`, the transfer capability advertises `result_policies` as `legacy` and `strict`. An unknown policy, or any nonempty policy for another type (including `tgfiles`), returns `400 Bad Request`. An empty string is treated as omission.

| Choice | Terminal state and hooks | Queries and webhooks |
| --- | --- | --- |
| Omitted or empty string | Keeps existing semantics; when errors are ignored, partial file failures can still produce completed / TaskSuccess | No result fields added |
| `legacy` | Keeps existing semantics | Creation echoes `result_policy`; queries and terminal webhooks can include counts |
| `strict` | All submitted files must succeed for completed / TaskSuccess; partial or total failure produces failed / TaskFail | Creation echoes `result_policy`; queries and terminal webhooks can include counts |

```json
{
  "type": "transfer",
  "storage": "local",
  "result_policy": "strict",
  "params": {
    "source_storage": "source",
    "source_path": "photos",
    "target_storage": "local",
    "target_path": "backup"
  }
}
```

This policy changes completion interpretation after execution. It preserves file scheduling: ordinary file errors can still allow remaining files to run, while cancellation and deadline errors follow the existing execution path. Saved files are not automatically rolled back or retried. Existing execution errors are preserved; only for a successful return does `strict` first check parent cancellation/deadline and then validate counts. User cancellation remains cancelled / TaskCancel, and deadline expiration remains failed / TaskFail. No terminal state is added. Telegram Bot and media-group flows do not use this option.

With `legacy` or `strict`, a task that entered and finished execution includes `result_policy` and `result_summary` in queries and terminal webhooks, for example:

```json
{
  "result_policy": "strict",
  "result_summary": {
    "total": 2,
    "pending": 0,
    "running": 0,
    "succeeded": 1,
    "failed": 1,
    "cancelled": 0,
    "interrupted": 0
  }
}
```

Each submitted file belongs to one state; the six state counts sum to `total`. Counts exclude files removed during preprocessing and contain no names, per-file errors, or retry manifest. If an operation returns cancellation/deadline, it is recorded as `cancelled` when the parent context has ended, otherwise as `interrupted`. This describes the observed state rather than proving a unique cause among concurrent events. Queued tasks and tasks cancelled before execution have no execution summary; absence does not mean every file succeeded.

`DELETE` reports cancelled immediately. If execution has not yet exited, that cancellation webhook may lack counts. After execution exits, queries can gain final counts and a new `updated_at`, while the terminal status stays cancelled and no second notification is sent. Webhook receivers should still deduplicate by task ID and terminal status.

---

### GET /api/v1/tasks — List All Tasks

Returns all tasks created via the API, ordered by `created_at` descending, then by `task_id` descending when timestamps match. Task records are stored in memory only and are cleared on restart.

**Response `200 OK`:**

```json
{
  "tasks": [
    {
      "task_id":    "abc123xyz",
      "type":       "directlinks",
      "status":     "running",
      "title":      "file.zip",
      "storage":    "local",
      "path":       "downloads",
      "error":      "",
      "created_at": "2026-03-11T10:00:00Z",
      "updated_at": "2026-03-11T10:00:05Z",
      "progress": {
        "total_bytes":      10485760,
        "downloaded_bytes": 5242880,
        "percent":          50.0
      }
    }
  ],
  "total": 1
}
```

The `progress` field is only included when `total_bytes > 0`. The `error` field is only included when non-empty.

---

### GET /api/v1/tasks/{task_id} — Get Task

**Path parameter:** `task_id` — the ID returned when the task was created.

**Response `200 OK`:** Same structure as a single task object from the list above.

**Error responses:**
- `400 invalid_request` — no task ID in path
- `404 task_not_found` — task does not exist

---

### DELETE /api/v1/tasks/{task_id} — Cancel Task

**Path parameter:** `task_id`

**Response `200 OK`:**

```json
{ "message": "task cancelled successfully" }
```

**Error responses:**
- `400 invalid_request` — no task ID in path
- `404 task_not_found` — task does not exist
- `500 cancel_failed` — cancellation failed

---

## Task Statuses

| Status | Meaning |
|---|---|
| `queued` | Task is queued and waiting to run |
| `running` | Task is currently executing |
| `completed` | Task finished successfully |
| `failed` | Task encountered an error |
| `cancelled` | Task was cancelled via the DELETE endpoint |

---

## Webhook Callbacks

When a `webhook` URL is provided in the create request, SaveAny-Bot sends a `POST` request to that URL when the task reaches a terminal state (`completed`, `failed`, or `cancelled`).

Terminal states cannot be overwritten by late progress or completion events. Cancelling a queued task also produces a notification. One notification is created per task, with at most three HTTP attempts and a 90-second overall limit; service shutdown cancels delivery and retry waits. Receivers should deduplicate by `task_id` and `status`. Notifications are not persisted and may be lost on shutdown or network failure. Cancellation requests that execution stop; already saved files are not necessarily removed.

**Callback request headers:**

```
Content-Type: application/json
User-Agent: SaveAny-Bot/1.0
```

**Callback request body:**

```json
{
  "task_id":      "abc123xyz",
  "type":         "directlinks",
  "status":       "completed",
  "storage":      "local",
  "path":         "downloads",
  "completed_at": "2026-03-11T10:01:00Z",
  "error":        ""
}
```

`completed_at` is only present when status is `completed` or `failed`. `error` is only present when non-empty.

**Retry policy:** Up to 3 attempts, with delays of 1s, 2s, and 3s between retries. Each request has a 30-second timeout.

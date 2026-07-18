---
title: "Copy History Messages"
weight: 5
---

# Copy History Messages

{{< hint warning >}}
This feature requires enabling UserBot integration.
{{< /hint >}}

Scan historical messages from a source channel/group, filter them, and forward whole messages to a target chat (optionally into a forum topic). Forward only — no download, no local storage.

## Usage

```
/copy <source_id> <target_id[:topicId]> [filter] [count]
```

| Parameter | Description |
|---|---|
| `source_id` | Source chat ID or username (required) |
| `target_id[:topicId]` | Target chat (required); `0` is not allowed; `:topicId` is a forum topic ID (see `/lstopic`) |
| `filter` | Optional; only `msgre:<regexp>` is supported; omit to match almost every message |
| `count` | Optional hit count, default 500, max 5000; collected newest-first until enough matches |

Examples:

```
/copy -1002229835658 -1003333444555
/copy -1002229835658 -1003333444555:12345 msgre:.*hello.*
/copy -1002229835658 -1003333444555 msgre:.*hello.* 100
/copy -1002229835658 -1003333444555 50
```

## Behavior

- Two-phase progress: scanning (`matched/count`) → forwarding (`done/total`); cancel via the button or `/cancel`
- Only one running `/copy` task per user at a time
- Albums are forwarded as a group; caption-less continuation segments may be included when message IDs are contiguous (does not count toward `count`, max 5)
- Forwarding is done by the **UserBot account** using copy mode (`DropAuthor`)
- **No** storage selection keyboard

## vs /watch

| | `/watch` | `/copy` |
|---|---|---|
| Timing | Live new messages | Historical backfill |
| Target `0` | Can save to default storage | Not supported |
| Output | Local storage or forward | Forward only |

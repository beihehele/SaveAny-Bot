---
title: "Copy History Messages"
weight: 5
---

# Copy History Messages

{{< hint warning >}}
This feature requires enabling UserBot integration.
{{< /hint >}}

Scan historical messages from a source channel/group, filter them, and copy them to a target chat via UserBot `forwardMessages` with `DropAuthor` (optionally into a forum topic). A clickable `[转]` link is prepended afterward. Deleting the source does not affect the copy. No download, no local storage.

## Usage

```
/copy <source_id> <target_id[:topicId]> [filter] [count]
```

| Parameter | Description |
|---|---|
| `source_id` | Source chat ID or username (required) |
| `target_id[:topicId]` | Target chat (required); `0` is not allowed; `:topicId` is a forum topic ID (see `/lstopic`) |
| `filter` | Optional `msgre:<boolean expression>`; omit to walk history newest-first |
| `count` | Optional hit count, default 500, max 5000; collected newest-first until enough matches |

### `msgre` expression

Prefix `msgre:`; the body is a **keyword boolean expression** (not a regexp):

| Symbol | Meaning |
|---|---|
| `&` | AND |
| `|` | OR |
| `!` | NOT (applies to the next term) |
| `()` | Grouping |

Matching is case-insensitive substring. Examples:

```
msgre:plana&(planb|planc|pland)
msgre:plana|planb|planc|pland
msgre:plana&vip&(!spam)
```

Examples:

```
/copy -1002229835658 -1003333444555
/copy -1002229835658 -1003333444555:12345 msgre:plana|planb
/copy -1002229835658 -1003333444555 msgre:plana&(planb|planc) 100
/copy -1002229835658 -1003333444555 50
```

## Behavior

- With keywords: Telegram server search (`messages.search`); without filter: history pagination
- Two-phase progress: scanning → sending; cancel via the button or `/cancel`
- Only one running `/copy` task per user at a time
- Matched messages are forwarded with `DropAuthor`; albums stay as one group; `[转]` is prepended by editing the caption
- `count` is per hit (an album counts as one hit)
- Performed by the **UserBot account**; original text formatting is preserved by forward

## vs /watch

| | `/watch` | `/copy` |
|---|---|---|
| Timing | Live new messages | Historical backfill |
| Target `0` | Can save to default storage | Not supported |
| Output | Local storage or DropAuthor copy | DropAuthor copy only |

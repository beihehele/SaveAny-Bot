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
- Progress edits have a 5-second budget and do not block copying on failure. Terminal feedback after cancellation also has its own 5-second budget.
- Cancellation is also checked after the final scan or forwarding feedback, so a last-item cancellation is not reported as success. Confirmed copies and their counts are retained without undo or automatic retry.
- Only one running `/copy` task per user at a time
- Each scan page waits at most 5 minutes by default, including reconnection and `FLOOD_WAIT`. Set `[telegram] scan_timeout = "10m"` or `SAVEANY_TELEGRAM_SCAN_TIMEOUT=10m` to adjust. A timeout fails the task without sending partial scan results or cancelling the shared Userbot client.
- Matched messages are forwarded with `DropAuthor`; albums stay as one group; `[转]` is prepended by editing the caption
- `count` is per hit (an album counts as one hit)
- Ordinary per-item forwarding failures are counted in the Bot completion reply while the loop continues. A completed task in the console does not mean every item succeeded; console history now retains aggregate counts after execution, with one message or album counted as one logical item. Unattempted items remain pending after cancellation; cancelled/interrupted attempts are included in the Bot reply’s failure count. A successful forward does not prove album completeness or a successful best-effort source-link edit. Scan failures do not publish partial selections; requested count is not the selected total.
- Performed by the **UserBot account**; original text formatting is preserved by forward

## vs /watch

| | `/watch` | `/copy` |
|---|---|---|
| Timing | Live new messages | Historical backfill |
| Target `0` | Can save to default storage | Not supported |
| Output | Local storage or DropAuthor copy | DropAuthor copy only |

Album lookup uses at most 49 IDs on either side of the representative message (99 IDs total), within one existing 100-ID fetch chunk for a resolved peer. Sparse/interleaved members within the window are deduplicated and sorted, without mixing group IDs. Cancellation, deadline and FLOOD_WAIT stop peer-alias retries. This is bounded best-effort selection: no expected member count is available, and a nonempty or single-member result never proves completeness or a known fallback. Members outside the window remain a limitation.

Scanning uses raw response page lengths and cursors. Service messages are excluded from copying without prematurely ending a full page. A repeated or non-advancing cursor fails the scan without forwarding partial selections.

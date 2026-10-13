---
title: "Watch Chats"
weight: 4
---

# Watch Chats

{{< hint warning >}}
This feature requires enabling UserBot integration.
{{< /hint >}}

Watch media messages from a source chat. When `target=0` (or omitted), messages are saved to default storage. When `target` is another channel/group ID, UserBot copies them with `DropAuthor` and prepends a clickable `[转]` link. Deleting the source does not affect the copy.

## Watch a chat

```
/watch <source_id> [target_id[:topicId]] [filter]
```

Examples:

```
/watch -1002229835658
/watch -1002229835658 0 msgre:hello|world
/watch -1002229835658 -1003333444555
/watch -1002229835658 -1003333444555:12345
/watch -1002229835658 -1003333444555:12345 msgre:plana&(planb|planc)
```

## List watches

```
/lswatch
```

Output format:

```
[id] <source_name> -> <target_name> [filter]
```

When `target` is `0`, the name is shown as **Local**. When a forum topic is set, the target is shown as `group#topic`. Use `/lschannel` and `/lsgroup` to look up names and IDs.

## List forum topics

```
/lstopic <group_id>
```

Output format:

```
topic name -> topicId
```

`topicId` is the forum topic ID (same as in `t.me/c/<chat>/<topicId>/...`), used in `/watch` `target:topicId` syntax. Get it from `/lstopic`.

## List channels / groups

```
/lschannel
/lsgroup
```

Output format:

```
<name> -> <id>
```

## Stop watching

```
/unwatch <id>
```

`id` is the auto-increment primary key from `/lswatch`.

A successful stop discards pending album buffers for that route. Even if a timer has already taken an album, local save tasks check the route again before queue admission. Recreating the same source and destination uses a new route identity and does not inherit old buffers. Other routes and topics are unaffected.

Userbot captures recipients when creating a media event, before its roughly five-second debounce and consumption queue. Refreshing a pending media/caption payload keeps the original recipient identities. Stopping and recreating a watch does not reassign old events to the replacement; other surviving recipients can still process them. Each captured route is checked again before admission.

This boundary uses event creation within this instance, not the message's original Telegram publication time. Messages first received after reconnect use the routes active at reception. If a watch starts receiving members midway through an album, forwarding still retrieves the selected media group as a whole rather than splitting it at watch creation time.

Stopping a watch does not cancel queued or running save tasks; cancel those separately from the task list. Waiting forwards are discarded by route. A forward starts when its worker slot passes the final route check; it may finish after the stop reply without interrupting the album. Reserved slots that have not passed that check still reject stopped routes. This boundary applies within one process; run only one instance for the same Bot/Userbot account.

## Filters

### msgre

After `msgre:` use a keyword boolean expression (`&` AND, `|` OR, `!` NOT, `()` grouping). Case-insensitive substring match. For example:

```
/watch -1002229835658 msgre:plana|planb
/watch -1002229835658 msgre:plana&(planb|planc)&(!spam)
```

## Forward notes

- Performed by the **UserBot account** via `forwardMessages` (`DropAuthor`); the Bot only handles management commands
- After copy, a `[转]` hyperlink is prepended by edit; original formatting is preserved
- Album hits are forwarded as a whole media group
- The source chat must be readable; the target chat must allow sending messages

## Forwarding capacity and order

Watch forwarding starts its own workers on demand: at most four occupied slots and 100 waiting logical items, with one message or album counted as one item. A full queue rejects new items immediately and records logs and diagnostic counts, without additional retries or restart replay. Local saves keep their existing queue, and `/copy` keeps its own task workflow.

Each target chat, across all its Topics, runs in admission order until forwarding and best-effort attribution finish. Waiting for a busy target does not occupy slots needed by other targets, although a full shared waiting queue can also reject new items for other chats. Plain and Bot API ID forms use a conservative equal-number scheduling key for current Telegram peer inputs; original IDs reach forwarding unchanged. Distinct peer namespaces with equal plain numbers may also run serially.

Admission order is not absolute Telegram source order: albums become ready independently after their buffers go quiet, and individual messages can become ready earlier. The existing forwarding function still retrieves and sorts album members, retaining any-member filtering, representative selection, caption, formatting, source links and Topic behavior. This queue does not serialize `/copy` or manual-client sends.

A successful `/unwatch` drops that route's buffers and waiting forwards, with identity checked again before execution. Started forwards, accepted local saves and other routes retain their lifetimes; fresh jobs for the same target wait for a started old job to finish. Shared service cancellation discards waiting forwards and stops workers. Neither the queue nor its observations persist.

Diagnostics show waiting/occupied/limit, admission/start/result, rejection and discard counts for this scheduler only. Upstream debounce, source-channel and album buffers are separate; this is not a whole-pipeline memory bound. Occupied slots include final validation, and a successful call does not prove album completeness or attribution edits.

## Media group acceptance checklist

This is an acceptance template, not the live test results for the current version. Use dedicated test content and source and destination chats, preferably with a dedicated Bot and UserBot session. Select checks for the features in use: a single-user deployment does not require a second user, while user isolation or multiple-topic checks need the corresponding users or topics. Record the tested version, source and destination message IDs, and expected and actual results in the version's acceptance report.

- [ ] **Incoming media and silent save**: Send photo and video albums and individual media; verify file counts, caption-based names, and save locations. For multiple-user deployments, also check concurrent submissions from two users without mixing their albums.
- [ ] **Overlapping albums**: Send two albums consecutively from one source. Watched saves and forwards keep album boundaries. Check order for consecutive, non-interleaved groups; also record readiness/admission order for interleaved groups without treating it as absolute source order.
- [ ] **Whole-album filtering**: A `msgre` match in one caption forwards the complete album. An album with no matching message is neither forwarded nor saved.
- [ ] **Default storage and album rules**: Saves with `target=0` follow existing directory, filename, and album rules, saving each file once.
- [ ] **Multiple destinations and topics**: Watch the same source into two destinations and into two topics in one forum group. Each destination receives one complete album without crossing topics.
- [ ] **Stop and recreate**: Stop one route while an album is buffered. Verify that unadmitted content is not saved or forwarded, other routes continue normally, and recreating the same source and destination does not inherit the old buffer. Accepted local saves and forwards that passed final validation may finish; waiting and reserved old-route items must not cross the stop boundary.
- [ ] **Attribution and formatting**: Use a caption with bold text, links, and emoji. Verify that `[转]` points to the correct source and preserves the original text and formatting.
- [ ] **Batch copy and cancellation**: Copy matching albums with `/copy` and cancel a separate copy task. Check complete albums, final status, cancel-button removal, and the ability to start another copy.
- [ ] **Rate limits and recovery**: Use controlled simulation or a naturally occurring `FLOOD_WAIT` to check wait reporting, cancellation, and final status. Leave this item unchecked if rate limiting was not exercised.

Automated regressions do not replace live checks for applicable scenarios. Record uncovered scenarios as unverified or not applicable; keep completed checks in the version's report rather than repeating them because this template remains unchecked.

Album lookup uses at most 49 IDs on either side of the representative message (99 IDs total), within one existing 100-ID fetch chunk for a resolved peer. Sparse/interleaved members within the window are deduplicated and sorted, without mixing group IDs. Cancellation, deadline and FLOOD_WAIT stop peer-alias retries. This is bounded best-effort selection: no expected member count is available, and a nonempty or single-member result never proves completeness or a known fallback. Members outside the window remain a limitation.

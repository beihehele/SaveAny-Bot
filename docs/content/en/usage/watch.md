---
title: "Watch Chats"
weight: 4
---

# Watch Chats

{{< hint warning >}}
This feature requires enabling UserBot integration.
{{< /hint >}}

Watch media messages from a source chat. When `target=0` (or omitted), messages are saved to default storage. When `target` is another channel/group ID, UserBot forwards them without downloading.

## Watch a chat

```
/watch <source_id> [target_id] [filter]
```

Examples:

```
/watch -1002229835658
/watch -1002229835658 0 msgre:.*hello.*
/watch -1002229835658 -1003333444555
/watch -1002229835658 -1003333444555 msgre:.*hello.*
```

## List watches

```
/lswatch
```

Output format:

```
[id] <source_id> <target_id> [filter]
```

## Stop watching

```
/unwatch <id>
```

`id` is the auto-increment primary key from `/lswatch`.

## Filters

### msgre

Regex-match the message text. For example:

```
/watch -1002229835658 msgre:.*hello.*
```

## Forwarding notes

- Forwarding is performed by the **UserBot account**; the Bot only handles management commands
- Uses copy mode (`DropAuthor`) so forwarded content is independent of the source
- Albums/media groups are forwarded in one batch to avoid splitting
- The source chat must be readable; the target chat must allow sending messages

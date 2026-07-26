---
title: "复制历史消息"
weight: 5
---

# 复制历史消息

{{< hint warning >}}
该功能需开启 UserBot 集成.
{{< /hint >}}

将源频道/群的历史消息按条件筛选后，经 UserBot **无来源复制**（`DropAuthor`）到目标聊天（可指定论坛话题），并在消息末尾追加可点击的 `[转自]` 链接。源消息删除不影响已复制内容。不下载、不落本地存储。

## 用法

```
/copy <source_id> <target_id[:topicId]> [filter] [count]
```

| 参数 | 说明 |
|---|---|
| `source_id` | 源聊天 ID 或用户名（必填） |
| `target_id[:topicId]` | 目标聊天（必填）；不支持 `0`；`:topicId` 为论坛话题 ID（见 `/lstopic`） |
| `filter` | 可选，`msgre:<布尔表达式>`；省略则按时间从新到旧取消息 |
| `count` | 可选，命中条数，默认 500，最大 5000；从新到旧凑够为止 |

### `msgre` 表达式

前缀 `msgre:`，冒号后为关键词布尔表达式（**不是正则**）：

| 符号 | 含义 |
|---|---|
| `&` | 与 |
| `|` | 或 |
| `!` | 非（紧跟一个词） |
| `()` | 分组 |

匹配不区分大小写（子串包含）。示例：

```
msgre:plana&(planb|planc|pland)
msgre:plana|planb|planc|pland
msgre:plana&vip&(!spam)
```

示例:

```
/copy -1002229835658 -1003333444555
/copy -1002229835658 -1003333444555:12345 msgre:plana|planb
/copy -1002229835658 -1003333444555 msgre:plana&(planb|planc) 100
/copy -1002229835658 -1003333444555 50
```

## 行为说明

- 扫描使用 Telegram 服务端搜索（有关键词时）或历史分页（无 filter）
- 进度分两阶段：扫描 → 发送，可点取消按钮或用 `/cancel` 停止
- 同一用户同时只能有一个进行中的 `/copy` 任务
- 命中后 `forwardMessages`（DropAuthor）；相册整组转发，再编辑 caption 追加可点击 `[转自]`
- `count` 按命中条数计（相册整组算 1 次命中）
- 由 **UserBot 个人账号** 执行；原文字样式随转发保留

## 与 /watch 的区别

| | `/watch` | `/copy` |
|---|---|---|
| 时机 | 实时监听新消息 | 回溯历史消息 |
| 目标 `0` | 可保存到默认存储 | 不支持 |
| 输出 | 本地存储或 DropAuthor 复制 | 仅 DropAuthor 复制 |

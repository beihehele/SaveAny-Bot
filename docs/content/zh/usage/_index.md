---
title: "使用帮助"
weight: 10
---

# 使用帮助

向 Bot 发送或转发媒体消息，或发送 Telegram 消息链接，即可选择本地、ShardReel 或 TG收藏 保存。`/save` 支持回复媒体保存及历史范围批量下载；静默模式使用默认存储和目录。

- [监听聊天](watch)：自动保存到本地或复制到目标聊天/Topic。
- [复制历史消息](copy)：按过滤条件复制历史消息。
- [`/collect` 快捷收藏](../deployment/configuration/storages/#tg收藏)：单个链接收藏到默认或临时指定的 Telegram 目标。
- [存储规则](rules)、[命名设置](config)、[静默保存](silent)。
- [网页管理](admin)、[HTTP API](api)、[命令行](cli)。

用 `/task` 查看任务，`/cancel` 取消任务。本地/ShardReel 文件目录相对于各自存储根目录；TG收藏 不使用目录。

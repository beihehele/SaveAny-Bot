---
title: "HTTP API"
weight: 20
---

# HTTP API

可选 API 与后台独立。配置 `[api]` 的 `enable=true`、监听地址、端口和非空 `token`。所有请求（含 `/health`）使用 `Authorization: Bearer <token>`。

| 方法和路径 | 用途 |
|---|---|
| `POST /api/v1/tasks` | 从 Telegram 消息链接创建本地保存任务 |
| `GET /api/v1/tasks` | 查看 API 任务 |
| `GET /api/v1/tasks/{id}` | 查询详情 |
| `DELETE /api/v1/tasks/{id}` | 取消任务 |
| `GET /api/v1/storages` | 查看已加载本地存储 |
| `GET /api/v1/task-types` | 当前可创建类型及客户端就绪情况 |
| `GET /health` | 健康检查 |

```json
{"type":"tgfiles","storage":"本机1","path":"albums","params":{"message_links":["https://t.me/c/123456789/123"]}}
```

`path` 是目标目录，单文件及媒体组均在其下按文件名保存。Bot/Userbot 必须能访问源消息；媒体组链接默认提取整组，`?single` 只保存指定成员。接口不创建监听或复制路由。

同一次提交中，重叠链接按聊天和消息 ID 去重，每条媒体消息只保存一次；不同聊天中的相同消息 ID 不会合并。`?single` 不展开媒体组，但同一次提交中另一个不带该参数的链接仍可选择整组。去重仅限本次请求，不影响之后的独立保存任务。链接查询阶段响应 HTTP 请求取消；取消后不会把已提取的部分文件入队，也不会取消共享 Bot/Userbot 客户端。

可选 `webhook` 接收任务终态事件；后台无 webhook 表单。`result_policy` 不适用于 Telegram 保存。任务、进度和近期结果只在内存中，重启清空，终态结果约保留 24 小时。任务被接纳后，HTTP 请求结束不会取消任务。


批量任务执行结束后，查询响应及正常终态 webhook 可包含 `result_summary`，统计 total/pending/running/succeeded/failed/cancelled/interrupted；failed 包括存储策略跳过的文件。这些计数仅描述逐文件结果，不改变任务终态或触发重试。未执行的任务或无法提供有效计数的任务省略该字段。提前取消的 webhook 可能不含最终计数，之后可查询补全结果，不会为补全再发一次 webhook。非空 `result_policy` 会明确拒绝。

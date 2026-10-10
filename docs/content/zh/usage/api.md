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

可选 `webhook` 接收任务终态事件；后台无 webhook 表单。`result_policy` 不适用于 Telegram 保存。任务、进度和近期结果只在内存中，重启清空，终态结果约保留 24 小时。任务被接纳后，HTTP 请求结束不会取消任务。

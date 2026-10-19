---
title: "HTTP API"
weight: 20
---

# HTTP API

可选 API 与后台独立。配置 `[api]` 的 `enable=true`、监听地址、端口和非空 `token`。所有请求（含 `/health`）使用 `Authorization: Bearer <token>`。

| 方法和路径 | 用途 |
|---|---|
| `POST /api/v1/tasks` | 从 Telegram 消息链接创建本地、ShardReel 或 TG收藏 保存任务 |
| `GET /api/v1/tasks` | 查看 API 任务及持久化 ShardReel 上传任务 |
| `GET /api/v1/tasks/{id}` | 查询详情 |
| `DELETE /api/v1/tasks/{id}` | 取消任务 |
| `GET /api/v1/storages` | 查看已加载存储及能力 |
| `GET /api/v1/task-types` | 当前可创建类型及客户端就绪情况 |
| `GET /health` | 健康检查 |

```json
{"type":"tgfiles","storage":"本机1","path":"albums","params":{"message_links":["https://t.me/c/123456789/123"]}}
```

TG收藏 忽略 `path`，只支持完整媒体组，不支持 `?single` 拆组；以下目录规则适用于本地和 ShardReel。`path` 是基础目标目录。普通文件直接保存在其下，媒体组按清理、截断后的 caption 建立子目录，同名 caption 共用目录；详见[存储端配置](../../deployment/configuration/storages/#shardreel)。Bot/Userbot 必须能访问源消息；媒体组链接默认提取整组，`?single` 只保存指定成员。接口不创建监听或复制路由。

同一次提交中，重叠链接按聊天和消息 ID 去重，每条媒体消息只保存一次；不同聊天中的相同消息 ID 不会合并。`?single` 不展开媒体组，但同一次提交中另一个不带该参数的链接仍可选择整组。去重仅限本次请求，不影响之后的独立保存任务。链接查询阶段响应 HTTP 请求取消；取消后不会把已提取的部分文件入队，也不会取消共享 Bot/Userbot 客户端。

可选 `webhook` 接收任务终态事件；后台无 webhook 表单。请求及任务参数只接受当前文档定义的字段和单个 JSON 值。普通任务、进度和近期结果只在内存中，重启清空。网页/API 共享最多 1000 条终态历史、最长约 24 小时，超限先清理最早更新的记录；淘汰后的详情查询返回 404。排队及仍在执行的任务（含取消收尾）不因历史额度或期限被淘汰。历史清理不影响已决定的 webhook 发送及重试。任务被接纳后，HTTP 请求结束不会取消任务。

ShardReel 任务和逐文件结果保存在 SQLite，查询附带 `upload_files`、可重试时的 `can_retry` 及取消收尾状态 `cancelling`。下载完成只是进入上传队列，最终结果及 webhook 等待远端入库校验；上传期间可通过上述删除任务接口请求取消。网页任务列表可取消进行中的任务，或删除已结束的记录；删除会同时放弃并清理待重试上传。已清理终态历史保留 7 天、最多 1000 条，失败、未完成或未清理的记录保留。`input_results` 和 webhook 注册仍在内存中，重启后不会补发丢失的 webhook；上传恢复不依赖它们。


ShardReel 任务下载期间，`downloading` 为 true，可用的 `progress` 表示 Telegram 下载进度；`upload_files[].uploaded_bytes` 仅表示上传字节数。下载交接后不再返回下载进度，最终完成仍以远端确认为准。

批量任务执行结束后，查询响应及正常终态 webhook 可包含 `result_summary`，统计 total/pending/running/succeeded/failed/cancelled/interrupted；failed 包括存储策略跳过的文件。这些计数仅描述逐文件结果，不改变任务终态或触发重试。未执行的任务或无法提供有效计数的任务省略该字段。提前取消的 webhook 可能不含最终计数，之后可查询补全结果，不会为补全再发一次 webhook。

创建响应与后续任务详情提供 `input_results`：`index` 对应提交链接的序号，`status` 为 ready/duplicate/failed/partial，`code` 表示选取阶段的原因，`new_files` 为本项新增选入数量。有效链接仍可入队，失败或重复项不会阻止其余链接；全部无可保存文件时返回 400 和对应结果，不创建任务。最多保留前 100 项，超出数量通过 `input_results_omitted` 表示，实际仍处理所有链接。结果不保存链接或原始 Telegram 错误，不等于下载完成或媒体组完整；API 原有获取媒体组失败时保留原消息的回退行为保持不变。Webhook 不增加这些选取字段。

已知整组查询失败或返回空结果时，对应项增加 `album_fallback: true`，表示只尝试选取原消息。该标记独立于选取状态：原消息新选入仍为 ready，之前已选入仍为 duplicate，无法保存仍为 failed；不据此推断丢失成员数量。没有已知降级时省略此字段；`?single` 以及查询成功但仅返回一个成员均不因数量被标记为降级。请求取消不触发回退，也不会接纳已准备的部分文件。

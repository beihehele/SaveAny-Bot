---
title: "HTTP API"
weight: 20
---

# HTTP API

SaveAny-Bot 提供了一套 HTTP API，允许你通过程序化方式创建下载/转存任务、查询任务状态、取消任务等，无需通过 Telegram 操作。

## 启用 API

在 `config.toml` 中添加或修改以下配置：

```toml
[api]
enable = true
host   = "0.0.0.0"   # 监听地址，默认 0.0.0.0
port   = 8080         # 监听端口，默认 8080
token  = "your-token" # 必填的鉴权 Token
```

也可通过环境变量覆盖（前缀 `SAVEANY_`）：

| 环境变量 | 对应配置项 |
|---|---|
| `SAVEANY_API_ENABLE` | `api.enable` |
| `SAVEANY_API_HOST` | `api.host` |
| `SAVEANY_API_PORT` | `api.port` |
| `SAVEANY_API_TOKEN` | `api.token` |

{{< hint warning >}}
启用 API 时必须设置非空 `token`；为空时 API 拒绝启动，Bot 仍可运行，请检查启动日志。
{{< /hint >}}

{{< hint warning >}}
API 创建的任务仅保存在**进程内存**中：进程重启后任务列表与进度会清空，正在执行的任务也会中断。不要依赖 API 任务做跨重启编排。
{{< /hint >}}

## 鉴权

所有 API 请求（含 `/health`）均需在 HTTP 请求头中携带 Bearer Token：

```
Authorization: Bearer <your-token>
```

鉴权失败时返回 `401`：

```json
{ "error": "unauthorized", "message": "invalid token" }
```

## 错误响应格式

所有错误均使用统一的 JSON 格式：

```json
{
  "error":   "error_code",
  "message": "错误说明"
}
```

常见错误码：

| 错误码 | HTTP 状态 | 含义 |
|---|---|---|
| `unauthorized` | 401 | 鉴权失败 |
| `method_not_allowed` | 405 | HTTP 方法不正确 |
| `invalid_request` | 400 | 请求体/参数非法 |
| `request_too_large` | 413 | 创建任务的请求体超过 1 MiB |
| `task_creation_failed` | 400 | 任务创建失败 |
| `task_not_found` | 404 | 任务 ID 不存在 |
| `cancel_failed` | 500 | 取消任务失败 |
| `internal_error` | 500 | 服务器内部错误 |

---

## 接口列表

### GET /health — 健康检查

需要鉴权。此接口只表示 HTTP 服务存活，不保证 Telegram、外部命令或存储可用。

**响应 `200 OK`：**

```json
{ "status": "ok" }
```

---

### GET /api/v1/storages — 列出存储

返回当前所有已加载的存储后端。

每项还返回 `readable`、`listable`、`stream` 和 `detect_existence` 布尔字段，分别表示接口支持读取、列举、流式上传及存在性检查。这些能力声明不代表外部服务已通过连通性验证。

**响应 `200 OK`：**

```json
{
  "storages": [
    { "name": "local",   "type": "local" },
    { "name": "MyMinio", "type": "s3" }
  ]
}
```

---

### GET /api/v1/task-types — 列出支持的任务类型

`types` 保留所有已实现的 API 任务类型。新增的 `capabilities` 数组为每种类型提供 `type`、`available` 和可选的 `reason`：检查 Aria2 配置、yt-dlp 程序、解析器注册和 Telegram 客户端初始化等本地前置条件，不执行下载或远端探测。可用不代表任意 URL、存储或凭据都能成功。

**响应 `200 OK`：**

```json
{
  "types": [
    "directlinks",
    "ytdlp",
    "aria2",
    "parseditem",
    "tgfiles",
    "tphpics",
    "transfer"
  ]
}
```

---

### POST /api/v1/tasks — 创建任务

请求体上限为 1 MiB，只接受一个 JSON 文档。提交前的解析、列举等操作收到请求 context，并设置 30 秒期限；JS 插件的匹配、纯 JS 执行与 `ghttp` 已响应取消。Playwright 在等待安装及各阶段之间检查取消，浏览器启动和导航使用更短的剩余期限，但已经开始的安装、驱动启动、页面操作与关闭，以及部分 Telegram 底层调用尚未完整响应取消，因此期限并非所有类型的硬保证。成功入队后的任务使用服务 context，HTTP 客户端断开不会取消它。

**请求头：**

```
Content-Type: application/json
Authorization: Bearer <token>
```

**请求体：**

```json
{
  "type":    "<任务类型>",
  "storage": "<存储名>",
  "path":    "<子目录>",
  "webhook": "<回调URL>",
  "params":  { }
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `type` | string | 是 | 任务类型，见下文 |
| `storage` | string | 是 | 目标存储名，须与配置中的存储名一致 |
| `path` | string | 否 | 存储内的子目录路径 |
| `webhook` | string | 否 | 任务完成、失败或取消时的回调地址 |
| `params` | object | 是 | 各任务类型的专属参数，见下文 |
| `result_policy` | string | 否 | 仅 `transfer` 支持；`legacy` 保留原终态并提供结果计数，`strict` 要求所有已提交文件成功；省略时沿用原接口行为 |

**响应 `201 Created`：**

```json
{
  "task_id":    "abc123xyz",
  "type":       "directlinks",
  "status":     "queued",
  "created_at": "2026-03-11T10:00:00Z"
}
```

#### 任务类型与 params

##### directlinks — 直接下载链接

下载一个或多个 HTTP/HTTPS 直链文件。

```json
{
  "type":    "directlinks",
  "storage": "local",
  "path":    "downloads",
  "params": {
    "urls": [
      "https://example.com/file.zip",
      "https://example.com/other.zip"
    ]
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `urls` | []string | 是 | 下载地址列表，至少 1 条 |

##### ytdlp — yt-dlp 视频下载

{{< hint warning >}}
需要在系统中安装 yt-dlp。
{{< /hint >}}

通过 yt-dlp 下载视频/音频，支持 YouTube、Bilibili 等 1000+ 网站。

```json
{
  "type":    "ytdlp",
  "storage": "local",
  "path":    "videos",
  "params": {
    "urls":  ["https://www.youtube.com/watch?v=xxx"],
    "flags": ["--extract-audio", "--audio-format", "mp3"]
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `urls` | []string | 是 | 媒体链接列表，至少 1 条 |
| `flags` | []string | 否 | 额外的 yt-dlp 命令行参数 |

##### aria2 — Aria2 下载

{{< hint warning >}}
需要在配置文件中启用并配置 Aria2 RPC。
{{< /hint >}}

通过 Aria2 下载管理器下载文件，支持 HTTP/HTTPS、FTP、BitTorrent（磁力链接、种子）等协议。

```json
{
  "type":    "aria2",
  "storage": "local",
  "path":    "downloads",
  "params": {
    "urls":    ["magnet:?xt=urn:btih:..."],
    "options": { "split": "4" }
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `urls` | []string | 是 | 下载地址列表，至少 1 条 |
| `options` | map[string]string | 否 | Aria2 下载选项 |

##### parseditem — 解析器下载

将 URL 交由已注册的 JS 插件或内置解析器处理后下载。

```json
{
  "type":    "parseditem",
  "storage": "local",
  "path":    "parsed",
  "params": {
    "url": "https://some-site.com/page"
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `url` | string | 是 | 待解析的页面 URL |

若没有任何解析器能处理该 URL，则返回 `400 task_creation_failed`。

##### tgfiles — Telegram 消息文件下载

通过 Telegram 消息链接下载文件。支持以下链接格式：

- `https://t.me/username/123` — 公开频道/群组
- `https://t.me/c/123456789/123` — 私有频道（数字 ID）
- `https://t.me/c/123456789/111/456` — 话题消息
- `https://t.me/username/111/456` — 用户名频道下的话题消息

若消息属于媒体组（相册），默认下载整组文件。在链接末尾追加 `?single` 可强制只下载单条消息的文件。

```json
{
  "type":    "tgfiles",
  "storage": "local",
  "path":    "telegram",
  "params": {
    "message_links": [
      "https://t.me/username/123",
      "https://t.me/c/1234567890/456"
    ]
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `message_links` | []string | 是 | Telegram 消息链接列表，至少 1 条 |

##### tphpics — Telegraph 文章图片下载

下载 Telegra.ph 文章中的所有图片。

支持的链接前缀：`https://telegra.ph/`、`http://telegra.ph/`、`https://telegraph.co/`、`http://telegraph.co/`

```json
{
  "type":    "tphpics",
  "storage": "local",
  "path":    "telegraph",
  "params": {
    "telegraph_url": "https://telegra.ph/Some-Article-01-01"
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `telegraph_url` | string | 是 | Telegra.ph 文章 URL |

##### transfer — 存储间文件传输

在两个存储后端之间直接传输文件，无需经过 Telegram。源存储须支持列举（list）和读取（read）操作。

{{< hint info >}}
`transfer` 任务中，顶层的 `storage` 字段仍然必须填写（用于通过参数校验），但实际使用的存储由 `params` 中的 `source_storage` 和 `target_storage` 决定。
{{< /hint >}}

```json
{
  "type":    "transfer",
  "storage": "local",
  "params": {
    "source_storage": "MyS3",
    "source_path":    "backups/",
    "target_storage": "LocalDisk",
    "target_path":    "restored/"
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `source_storage` | string | 是 | 源存储名 |
| `source_path` | string | 是 | 源存储中的路径，须包含至少一个文件 |
| `target_storage` | string | 是 | 目标存储名 |
| `target_path` | string | 是 | 目标存储中的路径 |

###### transfer 的结果策略

`result_policy` 是顶层字段，按请求选择，不修改全局配置或其他任务。`GET /api/v1/task-types` 的 `capabilities` 中，`transfer` 的 `result_policies` 列出 `legacy` 和 `strict`。其他类型（包括 `tgfiles`）选择非空策略，或任何类型填写未知策略，返回 `400 Bad Request`；空字符串按省略处理。

| 选择 | 终态与 hooks | 查询及 Webhook |
| --- | --- | --- |
| 省略或空字符串 | 保留原行为；允许继续时，部分文件失败仍可能 completed / TaskSuccess | 不增加结果字段 |
| `legacy` | 保留原行为 | 创建响应包含 `result_policy`；查询和终态 Webhook 可包含结果计数 |
| `strict` | 全部已提交文件成功才 completed / TaskSuccess；部分失败或全部失败为 failed / TaskFail | 创建响应包含 `result_policy`；查询和终态 Webhook 可包含结果计数 |

```json
{
  "type": "transfer",
  "storage": "local",
  "result_policy": "strict",
  "params": {
    "source_storage": "source",
    "source_path": "photos",
    "target_storage": "local",
    "target_path": "backup"
  }
}
```

策略只调整执行结束后的结果判定，不改变文件调度：普通单文件错误仍可继续处理剩余文件；取消和期限错误保持原执行路径。已保存的文件不会自动回滚，也不会自动重试。原执行错误保持不变；仅当执行返回成功时，`strict` 先检查父 context 的取消/期限，再检查结果计数。用户取消仍为 cancelled / TaskCancel，deadline 仍为 failed / TaskFail，没有新增终态。Telegram Bot 和媒体组流程不使用这个选项。

选择 `legacy` 或 `strict` 后，进入并完成执行的任务在查询及终态 Webhook 中附带 `result_policy` 与 `result_summary`，例如：

```json
{
  "result_policy": "strict",
  "result_summary": {
    "total": 2,
    "pending": 0,
    "running": 0,
    "succeeded": 1,
    "failed": 1,
    "cancelled": 0,
    "interrupted": 0
  }
}
```

每个已提交文件计入一种状态，六种状态之和等于 `total`。计数不包含预处理时排除的文件，不提供文件名、逐项错误或重试清单。文件操作返回取消/期限错误时，按记录当刻父 context 是否已经结束归入 `cancelled` 或 `interrupted`；这描述观察状态，不证明并发事件的唯一原因。排队或执行前取消的任务没有执行摘要，字段缺失不能解释为全部成功。

`DELETE` 会立即报告 cancelled；若当时执行尚未退出，该次取消 Webhook 可能没有摘要。执行退出后，查询可补充最终计数并更新 `updated_at`，保持取消终态，不再发送第二次通知。Webhook 接收端仍应按任务 ID 和终态去重。

---

### GET /api/v1/tasks — 列出所有任务

返回所有 API 创建的任务（仅在内存中保留，重启后清空），按 `created_at` 降序排列；创建时间相同时按 `task_id` 降序排列，保证返回顺序稳定。

**响应 `200 OK`：**

```json
{
  "tasks": [
    {
      "task_id":    "abc123xyz",
      "type":       "directlinks",
      "status":     "running",
      "title":      "file.zip",
      "storage":    "local",
      "path":       "downloads",
      "error":      "",
      "created_at": "2026-03-11T10:00:00Z",
      "updated_at": "2026-03-11T10:00:05Z",
      "progress": {
        "total_bytes":      10485760,
        "downloaded_bytes": 5242880,
        "percent":          50.0
      }
    }
  ],
  "total": 1
}
```

`progress` 字段仅在 `total_bytes > 0` 时出现。`error` 字段仅在有错误时出现。

---

### GET /api/v1/tasks/{task_id} — 查询任务

**路径参数：** `task_id` — 创建任务时返回的 ID。

**响应 `200 OK`：** 同上列表中的单个任务对象。

**错误响应：**
- `400 invalid_request` — 路径中未提供 task_id
- `404 task_not_found` — 任务不存在

---

### DELETE /api/v1/tasks/{task_id} — 取消任务

**路径参数：** `task_id`

**响应 `200 OK`：**

```json
{ "message": "task cancelled successfully" }
```

**错误响应：**
- `400 invalid_request` — 路径中未提供 task_id
- `404 task_not_found` — 任务不存在
- `500 cancel_failed` — 取消操作失败

---

## 任务状态

| 状态值 | 含义 |
|---|---|
| `queued` | 已入队，等待执行 |
| `running` | 正在执行 |
| `completed` | 已成功完成 |
| `failed` | 执行失败 |
| `cancelled` | 已通过 DELETE 接口取消 |

---

## Webhook 回调

创建任务时可设置 `webhook` 字段。当任务进入终态（`completed`、`failed`、`cancelled`）时，Bot 会向该地址发送一个 `POST` 请求。

终态不会被迟到的进度或结束事件改写；排队任务取消也会触发通知。每个任务只生成一次终态通知，HTTP 发送最多尝试三次、总时限 90 秒，服务退出会中止发送与等待。接收端按 `task_id` 和 `status` 去重；通知不持久化，退出或网络失败可能丢失。取消表示已请求停止执行，不保证所有已保存文件会被撤销。

**回调请求头：**

```
Content-Type: application/json
User-Agent: SaveAny-Bot/1.0
```

**回调请求体：**

```json
{
  "task_id":      "abc123xyz",
  "type":         "directlinks",
  "status":       "completed",
  "storage":      "local",
  "path":         "downloads",
  "completed_at": "2026-03-11T10:01:00Z",
  "error":        ""
}
```

`completed_at` 仅在状态为 `completed` 或 `failed` 时出现。`error` 仅在有错误时出现。

**重试机制：** 最多重试 3 次，重试间隔依次为 1 秒、2 秒、3 秒。每次请求超时为 30 秒。

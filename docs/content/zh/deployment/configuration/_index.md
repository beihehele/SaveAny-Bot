---
title: "配置说明"
---

# 配置说明

启动前准备 UTF-8 的 TOML 配置文件。默认读取工作目录的 `config.toml`；可用 `--config` 指定文件或 HTTP(S) 地址。缺少配置文件会报错，不会自动创建。环境变量使用 `SAVEANY_` 前缀和下划线分隔层级。

参考仓库的 `config.example.toml` 和 `config.example.full.toml`。

| 配置 | 用途 |
|---|---|
| `lang` | `zh-Hans` 或 `en` |
| `workers`、`threads`、`retry` | 并发任务、下载线程、保存重试 |
| `stream` | 默认 false；true 时流式下载并写入本地，节省缓存空间 |
| `telegram` | Bot token、API ID/Hash、RPC 重试、媒体组聚合时间 |
| `telegram.userbot` | 监听/复制所需个人账号及独立会话路径 |
| `telegram.proxy` | Telegram 连接代理 |
| `db`、`temp`、`cache` | 业务库、Bot 会话、下载临时目录和内存缓存 |
| `storages`、`users` | Local 根目录、授权用户及存储访问范围 |
| `hook.exec` | 任务开始、成功、失败、取消钩子及执行期限 |
| `api`、`admin` | 可选 HTTP API 与网页后台 |

业务库和会话需持久化；下载目录与配置/会话文件分开。用 `/storage` 选择默认存储；监听保存要求可用的默认存储。完整说明见[本地存储](storages)、[后台](../../usage/admin)和[API](../../usage/api)。

[精简版本迁移说明](../../usage/migration)列出旧配置检查和更新步骤。

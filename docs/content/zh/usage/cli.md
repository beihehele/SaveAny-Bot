---
title: "命令行子命令"
weight: 21
---

# 命令行

不带子命令启动 Bot：`saveany-bot --config config.toml`。

- `version`（别名 `v`）：查看版本、提交和构建时间。
- `admin-password`：交互生成管理员密码哈希；支持 `--stdin`。
- `help`、`completion`：命令帮助和 shell 补全。

全局选项包括 `--config`、`--lang`、`--workers`、`--retry`、`--threads`、`--stream`、`--proxy`、`--log-level` 及 Telegram、数据库、临时目录设置。以 `saveany-bot --help` 为准。

Telegram `/watch` 通过 Bot 管理聊天监听。Docker 默认入口启动 Bot，不转发 CLI 参数；查询版本或生成密码时使用 `--entrypoint /app/saveany-bot`。

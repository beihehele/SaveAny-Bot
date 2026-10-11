---
title: "日常运维与回退"
weight: 3
---

以下为 Linux Docker Compose 运维模板。先使用目标版本的 Compose 文件和配置，固定 `SAVEANY_IMAGE` 为已验收的版本或 digest。命令只用于你明确选定的部署目录；不能将 WSL 验收环境和服务器同时启动为同一 Bot/Userbot。

## 启动、停止与状态

```sh
set -eu
DEPLOY_DIR=/srv/saveany-bot
cd "$DEPLOY_DIR"
docker compose up -d
docker compose ps
docker compose exec -T saveany-bot /app/saveany-bot version
docker compose logs --tail=100 saveany-bot
docker compose stop --timeout 30 saveany-bot
```

`up -d` 可以启动或重建容器；`start` 只启动现有容器，不更新镜像。不要把完整配置、环境变量、session 或未经脱敏的日志贴入公开报告。

Compose 的 `restart: unless-stopped` 负责 Docker 启动后的容器恢复；Linux 服务器还需确认 Docker 服务随系统启动。使用 `systemctl is-enabled docker` 和 `systemctl is-active docker` 检查，按服务器管理方式启用。WSL 的发行版启动与 Windows 登录属于另一层，本模板不安装 Windows 登录任务或保活进程。人工停止的容器不会由该重启策略自动恢复。

启动后核对运行提交、容器健康、后台登录、挂载目录及队列；用允许清理的 Telegram 内容检查保存与 copy/watch。容器健康和 Telegram 初始化标记不能代替实际连通及任务结果检查。

## 停机备份

下面只覆盖仓库 Compose 的默认持久化布局。使用自定义配置、卷或外部路径时，先列全所有持久路径并相应修改清单；不能假定所有数据库、会话和下载文件都在默认 `data` 内。

```sh
set -eu
DEPLOY_DIR=/srv/saveany-bot
BACKUP_DIR=/srv/saveany-backups
cd "$DEPLOY_DIR"
docker compose stop --timeout 30 saveany-bot
SERVICE_ID=$(docker compose ps --all --quiet saveany-bot)
test -n "$SERVICE_ID"
test "$(docker inspect --format '{{.State.Running}}' "$SERVICE_ID")" = false
umask 077
mkdir -p "$BACKUP_DIR"
BACKUP_FILE=$(mktemp "$BACKUP_DIR/saveany-XXXXXXXX.tar.gz")
tar -czf "$BACKUP_FILE" -- config.toml docker-compose.yml .env data downloads cache
sha256sum "$BACKUP_FILE" > "$BACKUP_FILE.sha256"
sha256sum -c "$BACKUP_FILE.sha256"
```

此模板假设固定镜像写在 `.env`；若不使用 `.env`，从归档清单中去掉它，并另外记录实际镜像 digest。任何路径缺失或 `tar` 报错都表示备份失败，不得继续升级。还要核对其他实例/进程已停止写入，SQLite 的 WAL/SHM 随完整目录一起备份。备份目录应位于部署目录之外，并限制权限；备份包含账号会话与秘密配置，不随源码、镜像或公开验收报告发布。

## 更新与回退

1. 保存完整旧备份、旧镜像 digest 和旧版本 Compose/配置，确认备份可读取。
2. 更新 `.env` 的 `SAVEANY_IMAGE` 为候选固定版本，执行 `docker compose pull`、`docker compose up -d`，再核对提交和实际功能。仅重启不会更新镜像。
3. 失败时先停止候选及所有同账号实例。校验旧备份哈希，在独立恢复目录解包，核对持久路径和权限，使用旧配置、完整旧数据与旧镜像恢复；不要直接覆盖仍在运行的目录，不使用候选迁移后的库冒充旧备份。
4. 回退后核对版本、后台、用户偏好、监听路由/Topic、会话及保存文件，再以有限测试内容验收。回退成功前保留原始备份和失败现场。

迁移服务器时先停 WSL，再启服务器；反向回退时先停服务器。记录部署提交、镜像 digest、备份位置、检查结果和回退版本。目标服务器没有完成恢复及功能验收前，只能记录为候选部署。

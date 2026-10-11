---
title: "安装与更新"
---

# 安装与更新

## 从预编译文件部署(推荐)

使用本分支时, 二进制文件应来自[本仓库的 Release](https://github.com/beihehele/SaveAny-Bot/releases), 或从已验收的提交自行构建. 上游发布不包含本分支的全部修改.

在解压后目录新建 `config.toml` 文件, 参考 [配置说明](../configuration) 编辑配置文件

运行:

```bash
chmod +x saveany-bot
./saveany-bot
```

当前发布流水线提供 Linux/amd64 和 Windows/amd64 二进制，以及 Linux/amd64 容器。其他平台自行构建并验收，不在已发布支持范围；旧 OpenWrt 守护脚本已移除。

### 进程守护

{{< tabs "daemon" >}}
{{< tab "systemd (常规 Linux)" >}}

创建文件 <code>/etc/systemd/system/saveany-bot.service</code> 并写入以下内容:

{{< codeblock >}}
[Unit]
Description=SaveAnyBot
After=systemd-user-sessions.service

[Service]
Type=simple
WorkingDirectory=/yourpath/
ExecStart=/yourpath/saveany-bot
Restart=always

[Install]
WantedBy=multi-user.target
{{< /codeblock >}}

设为开机启动并启动服务:

{{< codeblock >}}
systemctl enable --now saveany-bot
{{< /codeblock >}}

{{< /tab >}}

{{< /tabs >}}


## 使用 Docker 部署

### 从源码构建镜像

在仓库根目录运行 `docker build -t saveany-bot:local .`。默认使用官方 Go 模块代理；网络不可达时可以显式指定可访问的代理，例如：

```bash
docker build --build-arg GOPROXY=https://goproxy.cn,direct -t saveany-bot:local .
```

构建参数只用于下载构建依赖，不改变运行时 Telegram 代理设置。

若要运行当前尚未发布的工作区，使用 `docker compose -f docker-compose.local.yml up -d --build`；它直接构建当前源码，无需 `SAVEANY_IMAGE`。

### Docker Compose

先检出与目标镜像相同的发布 tag/commit，使用该版本的 `docker-compose.yml` 和 `config.example.toml`，将配置另存为 `config.toml`。不要混用 `main` 分支示例。设置 `SAVEANY_IMAGE` 为已验收的完整镜像 tag 或 digest；也可在同目录 `.env` 文件中设置，未指定时 Compose 会报错。下面的 `YOUR_VERIFIED_VERSION` 必须替换为目标版本。

```bash
export SAVEANY_IMAGE='ghcr.io/beihehele/saveany-bot:YOUR_VERIFIED_VERSION'
```

启动:

```bash
docker compose up -d
```

### Docker

```shell
docker run -d --name saveany-bot --restart unless-stopped \
    -v /path/to/config.toml:/app/config.toml \
    -v /path/to/data:/app/data \
    -v /path/to/cache:/app/cache \
    -v /path/to/downloads:/app/downloads \
    "${SAVEANY_IMAGE:?Set SAVEANY_IMAGE to a verified tag or digest}"
```

{{< hint info >}}
本分支镜像只需要程序和配置获取工具。稳定部署应固定到已验收的镜像版本或 digest，并在隔离环境验证。
{{< /hint >}}

`data` 保存数据库和 Telegram 会话, 更新或重建容器时必须保留. `cache` 必须是仅用于临时文件的独立目录, 不要与 `data` 或 `downloads` 共用.

程序退出不再清空整个缓存目录，每个任务清理自己创建的临时文件。强制退出或超时可能留下残留；确认服务停止、目录用途和文件内容后再人工清理，旧的 `no_clean_cache` 配置和 `--no-clean-cache` 参数已移除，升级前应删除。

设置容器环境变量 `CONFIG_URL`（仅支持 HTTP(S)）时，程序直接加载远程配置，下载超时为 30 秒；HTTP 错误、响应不完整或配置解析失败会阻止启动。远程配置不会覆盖 `/app/config.toml` 或宿主机挂载文件，也不会保存为本地配置副本；未设置时继续读取本地配置。

## 更新

升级前先停止服务，将配置、整个 `data` 目录及私有部署文件复制到独立备份目录，包括数据库与 Bot/UserBot 会话。保留旧版程序或镜像 digest；不要删除数据库来规避迁移失败。回滚时停机恢复经过检查的完整备份与旧程序，不能假定迁移后的数据库一定兼容旧版。配置移除用户仍会触发既有的用户同步与关联数据删除，应先核对用户列表。

二进制部署：先备份配置和 data，停止服务，用本仓库已验收的发布文件替换程序后重启。升级前阅读迁移说明；保留原程序及数据备份以便恢复。

本版本不提供 Bot 或 CLI 自更新。二进制和 Docker 均通过人工替换经过验收的版本更新。

如果是 Docker 部署, 使用以下命令更新:

Docker 更新前确认 `data` 已持久化并备份. 旧容器未挂载 `/app/data` 时, 先停止容器, 用 `docker cp saveany-bot:/app/data /path/to/data-backup` 导出数据, 并将后续容器的 `/app/data` 绑定到这份数据. 保留原有代理和其他运行参数.

已按上面的挂载方式部署时:

```bash
docker pull "${SAVEANY_IMAGE:?Set SAVEANY_IMAGE to a verified tag or digest}"
docker stop saveany-bot
docker rm saveany-bot
docker run -d --name saveany-bot --restart unless-stopped \
    -v /path/to/config.toml:/app/config.toml \
    -v /path/to/data:/app/data \
    -v /path/to/cache:/app/cache \
    -v /path/to/downloads:/app/downloads \
    "${SAVEANY_IMAGE:?Set SAVEANY_IMAGE to a verified tag or digest}"
```

docker compose:

```bash
docker compose pull
docker compose up -d
```

仅重启已有容器不会切换到刚拉取的新镜像. Compose 的 `up -d` 会根据镜像变化重建容器并保留挂载数据, 见 [Docker 官方说明](https://docs.docker.com/reference/cli/docker/compose/up/). 更新前将 `SAVEANY_IMAGE` 改为此次已验收的版本或 digest，并核对匹配的配置。

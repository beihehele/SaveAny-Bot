<div align="center">

# <img src="docs/static/logo.png" width="45" align="center"> Save Any Bot

> **保存 Telegram 文件到本地，管理聊天监听和历史消息复制**

[![Release Date](https://img.shields.io/github/release-date/beihehele/SaveAny-Bot?label=release)](https://github.com/beihehele/SaveAny-Bot/releases)
[![tag](https://img.shields.io/github/v/tag/beihehele/SaveAny-Bot.svg)](https://github.com/beihehele/SaveAny-Bot/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/beihehele/SaveAny-Bot/build-release.yml)](https://github.com/beihehele/SaveAny-Bot/actions/workflows/build-release.yml)
[![Stars](https://img.shields.io/github/stars/beihehele/SaveAny-Bot?style=flat)](https://github.com/beihehele/SaveAny-Bot/stargazers)
[![Downloads](https://img.shields.io/github/downloads/beihehele/SaveAny-Bot/total)](https://github.com/beihehele/SaveAny-Bot/releases)
[![Issues](https://img.shields.io/github/issues/beihehele/SaveAny-Bot)](https://github.com/beihehele/SaveAny-Bot/issues)
[![Pull Requests](https://img.shields.io/github/issues-pr/beihehele/SaveAny-Bot?label=pr)](https://github.com/beihehele/SaveAny-Bot/pulls)
[![License](https://img.shields.io/github/license/beihehele/SaveAny-Bot)](./LICENSE)

</div>

## 功能

- 保存 Telegram 文档、图片、视频和媒体组到本地目录。
- 监听聊天、复制历史消息，保留过滤、媒体组边界和 Topic。
- 使用目录规则、命名模板及文件冲突策略整理本地保存。
- 通过 Telegram 或可选网页后台查询和取消任务。
- 支持多个命名 Local 根目录及用户存储访问范围。

## 快速开始

创建文件 `config.toml` 并填入以下内容:

```toml
[telegram]
token = "" # 你的 Bot Token, 在 @BotFather 获取
[telegram.proxy]
# 启用代理连接 telegram
enable = false
url = "socks5://127.0.0.1:7890"

[[storages]]
name = "本地磁盘"
type = "local"
enable = true
base_path = "./downloads"

[[users]]
id = 114514 # 你的 Telegram 账号 id
storages = []
blacklist = true
```

运行当前检出的代码前，先构建镜像：

```bash
docker build -t saveany-bot:local .
```

使用 Docker 运行：

```bash
docker run -d --name saveany-bot --restart unless-stopped \
    -v ./config.toml:/app/config.toml \
    -v ./data:/app/data \
    -v ./cache:/app/cache \
    -v ./downloads:/app/downloads \
    saveany-bot:local
```

`data` 保存数据库和 Telegram 会话, 重建容器时必须保留; `cache` 仅用于临时文件. 稳定部署应固定到已验收的镜像版本或 digest.


使用远端镜像时，选择已验收的 tag 或 digest，并使用对应发布 tag/commit 的配置和示例。远端 Compose 示例要求设置 `SAVEANY_IMAGE`；`latest` 可能对应不同产品版本。尚未发布的本地修改需自行构建。

请查看本分支的[安装与更新说明](docs/content/zh/deployment/installation.md)和[配置说明](docs/content/zh/deployment/configuration/_index.md). [上游文档网站](https://sabot.unv.app/)可能对应不同版本.

## 开发与测试


```bash
go test ./...
go vet ./...
```

真实 Telegram 媒体组联调使用独立 Bot、UserBot 会话和测试聊天，按[媒体组验收清单](docs/content/zh/usage/watch.md#媒体组验收清单)逐项检查。自动测试通过与真实联调通过应分别记录。

## 赞助

本项目受到 [YxVM](https://yxvm.com/) 与 [NodeSupport](https://github.com/NodeSeekDev/NodeSupport) 的支持.

如果这个项目对你有帮助, 你可以考虑通过以下方式赞助我:

- [爱发电](https://afdian.com/a/unvapp)

## 鸣谢

- [gotd](https://github.com/gotd/td)
- [TG-FileStreamBot](https://github.com/EverythingSuckz/TG-FileStreamBot)
- [gotgproto](https://github.com/celestix/gotgproto)
- [tdl](https://github.com/iyear/tdl)
- All the dependencies, contributors, sponsors and users.

## 社区和关于作者

- [![通知群组](https://img.shields.io/badge/ProjectSaveAny-Group-blue)](https://t.me/ProjectSaveAny)
- [![讨论区](https://img.shields.io/badge/Github-Discussion-white)](https://github.com/beihehele/SaveAny-Bot/discussions)
- [![个人频道](https://img.shields.io/badge/Krau-PersonalChannel-cyan)](https://t.me/acherkrau)

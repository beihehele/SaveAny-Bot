---
title: "网页管理后台"
weight: 21
---

# 网页管理后台

后台默认关闭，启用后使用固定账号 `admin` 登录。它是本实例管理员入口，与 HTTP API 的 Bearer Token 相互独立，不需要同时启用 API。

## 启用与登录

在配置文件加入：

```toml
[admin]
enable = true
host = "127.0.0.1"
port = 8081
password = "replace-with-your-long-password"
session_ttl = "12h"
secure_cookie = false
```

密码需为 12–1024 字节，没有默认密码。保存配置后人工重启，打开 `http://127.0.0.1:8081/admin/`。后台在 Bot 初始化后启动；Telegram 登录前的错误仍查看容器日志。后台的凭据、端口等验证失败会拒绝启动后台，Bot 继续运行；不能解析的配置文件仍会使应用初始化失败。

建议使用哈希代替配置中的明文密码：

```sh
saveany-bot admin-password
```

Docker 镜像的默认入口启动 Bot，不转发 CLI 参数。生成哈希时使用 `docker run --rm -it --entrypoint /app/saveany-bot <你的镜像> admin-password`，或在运行中的容器内执行 `/app/saveany-bot admin-password`。

此命令不启动 Bot，也不读取部署配置。在终端输入并确认密码，将输出的 `password_hash = "..."` 放到 `[admin]` 中并删除 `password`。仅接受此命令生成的有界 Argon2id 格式。若通过秘密文件自动化设置，可用 `saveany-bot admin-password --stdin < /path/to/password-file`；命令只去掉末尾一个换行，保留密码中的空格，不要将密码写进命令行参数或 shell 历史。

支持 `SAVEANY_ADMIN_ENABLE`、`SAVEANY_ADMIN_HOST`、`SAVEANY_ADMIN_PORT`、`SAVEANY_ADMIN_PASSWORD`、`SAVEANY_ADMIN_PASSWORD_HASH`、`SAVEANY_ADMIN_SESSION_TTL`、`SAVEANY_ADMIN_SECURE_COOKIE` 环境变量。`password` 与 `password_hash` 必须且只能设置一个；有效期支持 `5m` 至 `24h`，默认 `12h`。

## Docker 与内网访问

项目默认 Compose 使用 host 网络，`127.0.0.1` 监听适用于宿主机本地访问。若自建 bridge 网络 Compose，需让容器内监听 `0.0.0.0` 并发布端口到宿主机 `127.0.0.1:8081:8081`。不要仅修改容器内监听地址就认为端口已经发布。

如需局域网访问，显式配置监听范围，并通过 HTTPS 反向代理访问，设置 `secure_cookie=true`。代理必须保留原始 Host，不跨域代理管理接口；后台不信任任意 `X-Forwarded-*` 请求头。`secure_cookie=true` 后使用 HTTP 将无法维持登录。原 API Token 不会授予后台访问权，后台 Cookie 也不能替代 API Token。

当前旧的 GHCR `latest` 镜像不一定包含本功能。应先构建包含后台改动的镜像，并记录明确版本，不依赖旧镜像标签测试新功能。

## 功能与数据范围

- 总览：版本、提交、后台运行时长、队列及已加载存储。任务能力只检查本地前置条件，不保证远端可用。
- 任务：队列中 Bot/API 活动任务，以及网页/API 任务的详细进度和近期结果。搜索、筛选、分页和取消；取消中的任务需等到 worker 释放后才完成。不经过队列的监听转发不在列表中。
- 新建任务：输入 Telegram 消息链接并选择本地保存目录；客户端未就绪时禁用。网页不提供任意命令或 webhook 表单。任务接纳后，关闭页面不会取消它。
- 存储/本地文件：能力查询，本地目录分页浏览与附件下载（支持 Range）。不提供文件修改；已知配置、业务库、Bot/UserBot session 库及 sidecar 不能下载，即使通过文件别名访问。管理员可以访问本地存储根目录内其他文件，因此应将保存目录与私有部署文件分开。
- 监听路由、保存设置：只读展示已有用户的路由/topic/filter、目录、规则、命名和冲突策略。通过已有 Bot 命令修改；后台不创建/删除 Telegram 用户，不改变媒体组处理行为。
- 诊断：白名单运行配置和最多 100 条管理操作。不提供任意日志文件读取，不返回密码、token、session 或完整部署配置。

任务记录和审计在内存中，重启清空。网页/API 任务的终态结果约保留 24 小时；普通队列任务完成后不保留详细历史。会话最多 16 个，新增会话超限会淘汰最早到期的会话；退出或服务重启使旧会话失效。单个客户端地址每分钟最多 5 次登录尝试，代理后的客户端可能共享该额度。

中英文界面资源随二进制内嵌，无需 CDN、Node 运行环境或额外前端容器。浏览器只保存语言偏好，登录 Cookie 为 HttpOnly；写操作校验同源 Origin 和会话绑定的 CSRF token。

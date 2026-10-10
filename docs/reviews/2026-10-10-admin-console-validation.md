# 管理后台本地验收与最终审查
> 历史记录：本文对应 Telegram-only 精简之前的后台版本；其中外部下载、解析、转存及旧构建组合不代表当前功能。当前范围与验证以 [Telegram + Local 清理报告](2026-10-10-telegram-only-cleanup.md) 为准。

日期：2026-10-10。基线：dev `bca55455b330a45eeb7f50b683a887d4dea956cb`。本次修改保持未提交、未暂存、未推送状态。

## 交付范围

固定账号 admin，配置文件明文密码或有界 Argon2id 哈希；独立监听端口，默认关闭。提供总览、任务、新建任务、存储、本地文件、监听路由、保存设置、系统诊断及中英文界面。任务提交/取消、本地下载可操作，路由/规则/偏好/部署配置只读。

沿用现有 TaskFactory、队列、用户同步和本地存储；未修改 core、storage 或 Telegram 媒体组 handlers。现有 API Token 和后台 Cookie 相互独立。网页/API 详细记录和审计只在内存中保留；非队列转发不纳入可取消任务。

## 验证结果

| 验证 | 结果 |
| --- | --- |
| `go test ./...`、`go vet ./...` | 通过；后续新增测试与 UI 调整已单独复测 |
| admin/adminauth/config/api/local storage/Bot handlers 的 race | 通过 |
| 最终后台任务/文件/只读偏好集成测试的 race | 通过，含运行中取消未释放 worker 的状态检查 |
| core race | 首次既有 Windows 后台子进程句柄测试失败，独立全包重跑通过；保留首次失败记录，没有修改或放宽该测试 |
| no_playwright、sqlite_glebarez 相关回归 | 通过 |
| Windows 完整/micro/pico、Linux 完整构建 | 通过 |
| JavaScript 语法、翻译键、Go 格式、diff 空白 | 通过 |
| 浏览器桌面与实际 390px 手机视口、八页面和中英文 | 通过，无页面横向溢出 |
| 浏览器错误密码/登录/退出、直链保存、运行任务取消、Range、空搜索、详情 | 通过，使用合成数据库和本机 HTTP 源 |
| DOM 注入与 CSRF | HTML 测试标题显示为文字，未创建图片节点；未带 CSRF 的写请求被拒绝；Cookie 为 HttpOnly，浏览器仅持久化语言偏好 |
| WSL 原 Dockerfile 构建 | 通过；生产代码与当前工作区核对一致 |
| Alpine 运行镜像内 admin/core/database/handlers 回归 | 通过：分别 5/48/5/111 个 PASS 输出，包含子测试，不能解读为独立顶层测试数量 |
| 隔离容器内 HTTP 与文件/任务集成 | 16 项检查通过，覆盖 CLI、鉴权、脱敏、任务保存/取消、Range、注销及 SIGTERM 正常退出；退出码 0、无 OOM，测试容器已清理 |

Docker CLI 首次测试直接向默认入口传递参数，因入口不转发参数而失败。已改用显式 `/app/saveany-bot` entrypoint 验证，并在中英文说明中记录正确命令；生产入口脚本未改动。

## 最终审查

核对了默认关闭和后台启动失败隔离、密码长度/哈希成本/限速/会话上限、Origin/CSRF 校验、诊断字段白名单、本地文件 os.Root 约束、配置/数据库/session 及 sidecar 和别名保护。队列任务沿用服务 context，HTTP 响应结束不会取消已接纳任务；运行任务的取消状态保留至 worker 释放。

修复了刷新时表格按钮丢失键盘焦点、语言切换异步覆盖、防重复提交和分页越界等交互问题。所有动态任务和文件文本使用 textContent，静态资源内嵌，不依赖 CDN 或 Node 运行环境。

未发现本次范围内需要继续修复的阻塞问题。原有 Windows 子进程测试出现一次不稳定失败，复测通过，不能据此声称首次全套执行零失败。

## 验收边界与运行证据

没有隔离 Telegram 账号/聊天，因此未进行真实 Bot 登录、Telegram 下载、媒体组转发和 topic 路由联调。HTTP 集成夹具初始化真实 config/database/storage/core/admin，使用合成账号和本机下载源，跳过 Telegram 客户端登录；不能代替完整生产启动验收。生产配置和数据未读取或修改，原有未跟踪草稿哈希保持不变。

测试日志、构建产物、隔离夹具和检查 JSON 位于被忽略的 `tmp-architecture-validation-20261009/_admin-console-20261010/`；WSL 证据位于 `/root/saveany-validation/20261010-admin-local/evidence/`。候选镜像为本机 `saveany-admin-local:runtime`，版本标记 `admin-local`、提交标记 `bca5545-dirty`，未上传仓库。保存历史、原参数重提和设置/路由写入属于后续设计范围。

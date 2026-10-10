# 管理后台首版设计
> 历史记录：本文对应 Telegram-only 精简之前的后台版本；其中外部下载、解析、转存及旧构建组合不代表当前功能。当前范围与验证以 [Telegram + Local 清理报告](2026-10-10-telegram-only-cleanup.md) 为准。

## 使用场景与交付范围

单管理员、单实例、默认仅本机访问。固定账号 `admin`，密码或密码哈希由配置文件提供。管理入口 `/admin/`，独立端口默认 8081；与现有 Bearer Token API 的启用、鉴权相互独立。管理员有本实例任务和已加载存储的管理权限，不增加 Telegram 用户或更改用户同步机制。

首版交付总览、任务中心、新建任务、存储、本地文件、监听路由、保存设置和系统诊断。任务创建、取消和本地下载可操作；路由、规则、偏好和部署配置只读。默认关闭后台，配置保存后通过人工重启应用。

| 页面 | 内容与操作 | 边界 |
| --- | --- | --- |
| 总览 | 版本/提交、后台运行时间、运行/排队数量、存储数量、任务能力 | 已加载不代表远端健康；运行时间是管理服务启动后的时间 |
| 任务中心 | 合并队列活动任务与网页/API 跟踪记录；搜索/状态筛选、进度、详情、取消 | 队列快照只有 ID/标题/时间；网页/API 记录在内存中，终态保留约 24 小时，重启丢失 |
| 新建任务 | 能力驱动的直链、网站解析、视频、Telegram、Telegraph、转存、Aria2 表单 | 调用现有 TaskFactory，保留服务 context、取消、hooks 和结果策略；后台不提供任意命令或 webhook 表单 |
| 存储 | 名称、类型、读取/列举/存在性检测等能力 | 只显示已加载实例，不暴露存储凭据 |
| 本地文件 | 存储/目录选择、分页、下载；明确只读 | 限已加载本地存储，沿用 os.Root 路径保护；禁止访问配置和已知 session/业务库及 sidecar；附件下载而非 HTML 预览 |
| 监听路由 | 来源、目标、topic、过滤条件，按 Telegram 用户展示 | 不编辑路由，不触及相册 buffer、caption/filter、copy/转发时序；非队列转发不冒充可取消任务 |
| 保存设置 | 现有用户的目录、规则、默认存储、命名、冲突策略、静默偏好 | 只读；不创建/删除用户 |
| 系统诊断 | 明确字段白名单的运行配置、依赖能力、近 100 条管理操作 | 不读取任意日志文件，不返回密码、token、完整配置、hooks 命令或 session 内容；审计在内存中 |

## 交互设计

桌面左侧导航、顶部页面标题和刷新/退出，手机导航折叠为横向菜单。浅色背景、深色侧栏、青绿色操作按钮。支持简体中文/英语，所有交互文案由内嵌翻译资源提供；语言偏好可存浏览器，本地不存登录凭证。

页面状态完整覆盖加载、空数据、错误、能力不可用和会话失效。任务页面约 3 秒刷新，后台标签页暂停刷新，避免并发刷新覆盖新页面。提交按钮防重复点击；取消需确认，运行中任务显示“正在取消”直到 worker 释放。文件下载用原生同源附件链接，支持 Range，不将大文件整体载入内存。

## 登录与接口

支持 `admin.password` 便于直接设置，推荐 `admin.password_hash`；两者互斥，不提供默认密码。明文模式仅启动时派生哈希，磁盘配置仍含明文，应只读挂载并限制权限。哈希为固定且有界的 Argon2id 参数（19 MiB、2 次、1 lane），用交互式 `admin-password` 命令生成，不通过命令行参数传入密码。

随机会话 ID 由服务端维护；HttpOnly、SameSite=Strict、/admin/ 范围 Cookie，HTTPS 配置 `secure_cookie=true`；默认绝对有效期 12h。注销/重启使会话失效，会话数量和登录限速表有界。登录需要同源 Origin，其他写操作同时验证同源 Origin 和会话绑定 CSRF token；KDF 并发有界，失败不会泄漏账号信息。

| 接口 `/admin/api/v1/` | 行为 |
| --- | --- |
| POST login / POST logout / GET session | 建立/注销会话，获取 CSRF 和到期时间 |
| GET overview | 版本与队列计数 |
| GET tasks / POST tasks / POST tasks/{id}/cancel | 活动任务/跟踪结果，复用任务工厂提交、核心队列取消 |
| GET storages / GET task-types | 复用现有能力查询 |
| GET files / GET download | 本地受限列表、流式附件下载 |
| GET preferences | 现有用户、目录、规则、路由的只读投影 |
| GET diagnostics | 白名单运行配置和有界审计记录 |

响应默认 no-store；CSP 限同源脚本/样式，禁止 iframe，禁止跨域读取，不接入第三方 CDN。错误/任务文本脱敏，动态内容使用 textContent 渲染。下载及诊断防止已知配置/数据库通过本地文件入口暴露。

## 集成与验收

新增独立 `admin` 模块和内嵌 HTML/CSS/JS/翻译资源，不需要 Node 或新运行容器。启动在现有 Bot/API 初始化后、workers 启动前，后台启动失败记录错误而不停止稳定 Bot。保留现有启动顺序，因此 Telegram 登录前发生的故障仍依赖容器日志。

验收包括密码配置/环境变量、正确与错误登录、限速、过期/注销/重启、CSRF/跨域、路由鉴权、任务创建/取消及 API Token 隔离、本地路径越界/symlink/数据库保护/Range、脱敏和 DOM 注入、桌面/手机页面。执行全量 Go test/vet、相关 race、构建变体，WSL Docker 内回归及浏览器实际操作；使用合成配置和隔离服务，不读取生产数据，不自动提交。

第二阶段才评估跨重启历史、原参数重提、偏好/规则写入、路由变更期间的相册处理及部署配置候选导出。

参考：[OWASP 密码存储](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)、[会话管理](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)、[CSRF](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)、[Go embed](https://pkg.go.dev/embed)。

# Telegram + Local 精简实施与整体审查

日期：2026-10-10。基于 `dev` 的 `bca55455b330a45eeb7f50b683a887d4dea956cb` 及实施前已有的未提交管理后台代码。此次不合并 `main`，不提交、不推送、不切换生产部署。

## 最终范围

保留 Telegram Bot/Userbot、实时监听、历史复制、Topic、媒体组、过滤、caption、Telegram 文件本地保存、文件命名、目录与规则、冲突策略、队列、取消和生命周期 hooks。保留多个命名 Local 根目录、原有用户权限、管理后台及独立 Bearer API。

Bot 保留 19 个命令：`/start`、`/silent`、`/storage`、`/dir`、`/rule`、`/save`、`/task`、`/cancel`、`/config`、`/fnametmpl`、`/help`、`/watch`、`/copy`、`/unwatch`、`/lswatch`、`/lschannel`、`/lsgroup`、`/lstopic`、`/syncpeers`。注册表、中英文帮助和命令菜单使用同一来源；新增回归核对命令与帮助。

已删除网站下载与解析、Telegraph、Aria2、yt-dlp、JS/浏览器插件、远端存储和 Telegram 再上传的实现、专属任务、命令、配置、测试及使用文档。CLI 删除 `upload`、`watch`、`upgrade`，保留运行、版本和管理员密码工具；旧命令不会落入 Bot 启动入口。镜像配方只安装仍被配置下载使用的 curl，删除 FFmpeg/ffprobe/yt-dlp。CI 保留普通测试、竞态、vet、模块校验、两种 SQLite 实现和跨平台构建，删除已不存在的功能构建组合。

管理网页只提供 Telegram 保存任务表单。监听路由、保存设置仍只读；本次没有新增网页监听/复制编辑功能。API 只创建 `tgfiles`，Bot `/copy` 和队列中的复制任务仍保留。

## 保护边界与审查结论

- 按实施前源文件快照逐文件核对 42 个保留链路生产文件的 SHA-256：Userbot、watch 系列、媒体组、复制、单/批下载、Local 写入、重试、中间件与核心 worker 实现一致。共享任务结果、进度、取消和 hooks 框架保留，避免为删除外部功能改变 Telegram 语义。
- Telegram 注册与存储选择公共入口只移除外部功能分支；Telegram 链接、媒体、相册批保存和回调路径保留。
- 复核发现 API 将目录直接交给需要完整文件路径的任务构造函数。现统一拼接各自文件名，单文件和相册采用相同目录语义；只修改 API 适配层。新测试以模拟 Telegram RPC 输入运行真实 factory、队列、下载、Local 写入和进度/取消链路。
- 配置检查拒绝启用的旧功能和非 Local 后端。新增只读数据库检查报告失效默认存储、目录和规则引用，不替换存储、不修改路由或数据库结构；`CHOSEN` 规则保留原义。原用户自动同步流程不变。
- 启动前检查发生在 Telegram 登录和 worker 启动之前。禁用的历史解析器/Aria2/存储块可在迁移期间忽略；旧 yt-dlp 配置明确拒绝。最终配置应删除这些历史块。
- 清理了 35 个无用模块，没有升级仍保留的模块版本。`go mod tidy` 将原 `go.sum` 已锁定的 `gopkg.in/check.v1` 显式列为间接依赖。中英文翻译仅保留实际使用的 212 个键并重新生成索引。
- 当前配置及使用文档不再宣称被移除的能力。迁移说明、拒绝旧配置/任务的测试及历史审查报告仍可提及旧名称，它们不提供旧功能实现。用户原有网盘评估草稿保持原样。

截至本次整体 diff 审查，没有发现新引入且尚未修复的代码合入阻塞项；这不等同于真实服务及发布部署已经验收。

## 验证记录

| 检查 | 结果及范围 |
| --- | --- |
| 实施前全量测试 | 通过，作为保留功能基线 |
| 分批及最终全量普通测试 | 最终 `go test -work ./...` 通过；API、数据库引用检查和字符串处理改动也分别复测通过 |
| 全量竞态测试 | `go test -work -race ./...` 通过 |
| 静态检查 | 最终 `go vet ./...` 通过 |
| 静态构建 | Windows/amd64、Linux/amd64 及 Linux 备用 SQLite 构建通过，均为 `CGO_ENABLED=0`；版本命令通过，已删除 CLI 命令拒绝执行 |
| 依赖完整性 | `go mod verify` 通过 |
| 可达漏洞扫描 | `govulncheck ./...` 通过：代码和导入包均为 0 条；模块层面另有 11 条公告未进入实际导入包/调用路径，本次未升级核心依赖 |
| SQLite 兼容 | `go test -work -tags=sqlite_glebarez ./database` 通过，包括备份恢复 |
| Telegram 本地保存端到端 | 单文件/相册 × 缓冲/流式，以及失败、取消，共 6 类自动场景通过；Telegram RPC 为模拟输入 |
| Bot 保留行为 | 既有媒体组、监听转发、过滤、命名、Topic、复制、取消、hooks 和重试回归随全量/竞态测试通过 |
| 管理后台 | 认证、CSRF、文件与目录边界、下载和任务生命周期自动测试通过；隔离浏览器预览验证中文 8 页、英文新建任务及能力收敛，没有发现脚本错误 |
| 入口脚本 | Git Bash 中 `tests/entrypoint_test.sh` 通过 |
| 文档 | 官方校验和验证的 Hugo 0.147.8 构建通过；41 个 HTML 页内部路径和锚点检查无失效引用 |
| 工作区格式 | `git diff --check` 通过 |
| 本次候选 Docker 镜像 | 已完成 WSL 本地构建及实际 Alpine runtime 自动回归；镜像标识、源码核对、网络边界及隔离重启见下文 |
| 真实 Telegram 联调 | 按用户明确限定完成生产 Bot API 只读连通检查，见下文；仍无隔离 Bot/Userbot 和聊天环境，未验收真实监听、复制和媒体组 |

Windows 测试采用 `-work` 保留本次拥有的临时编译目录，避免测试完成后的 exe 删除被文件映射阻止。临时编译目录被隔离为独立模块，避免项目 `./...` 扫入构建产物。曾出现修改测试期间的编译输入不一致及临时产物误扫描；冻结 Go 修改、隔离产物后完整竞态和 vet 复测通过。

### 本轮复审与 WSL 补充验证

复审再次发现并修复了以下遗漏；不是保留 Telegram 功能的重构：

- 删除无人引用的 `Dockerfile.micro` / `Dockerfile.pico`，彻底移除旧功能编译标签组合。保留主 Dockerfile 与备用 SQLite 自动构建验证。
- 中英文安装说明删除旧自更新流程、旧 Bot 按钮及备份程序说明；改为人工更新，补充源码构建与可选模块代理参数。已有后台设计/验收记录明确标为精简前历史记录。
- 删除无人调用的 `HashString`、`FormatSize`、上传分片/照片限制常量及无用导入；下载分片常量值保持不变。清理 5 个遗漏空目录及 API 文件中的乱码注释。
- Docker 模块下载和编译统一缓存到 `/go/pkg/mod`。增加可选 `GOPROXY` 构建参数，默认仍为 `https://proxy.golang.org,direct`。本机使用 `https://goproxy.cn,direct` 成功下载模块；不改变运行时 Telegram 代理。

最终增量相对实施前源码快照为 90 个修改、156 个删除、11 个新增文件（包含此前未提交后台的本次修改；不等同于相对 Git HEAD 的统计）。42 个关键保留链路生产文件及用户原有评估草稿继续逐字节一致。构建依赖包图无被移除功能的项目包或 SDK；同名通用配置/过滤语法解析器属于保留功能。兼容拒绝检查、负向测试和历史记录保留旧名称，均没有对应旧功能入口。

WSL：Ubuntu 24.04 / Linux 5.15.153.1 WSL2 / Docker 29.9.0。未改动宿主机或 Docker 守护进程配置。

| WSL 检查 | 实测结果 |
| --- | --- |
| 固定基础镜像拉取 | 公共入口 `docker.m.daocloud.io/library/...` 的 `docker pull` 成功，Go/Alpine 摘要与主 Dockerfile 固定值一致；复用已缓存层。Docker Hub 和 Google 镜像入口直连超时，不能宣称直连已修复 |
| 最终 builder/runtime | 使用本次冻结源码及真实 Dockerfile 成功构建；没有发布或推送镜像 |
| Linux 全量测试/静态检查 | 最终 `CGO_ENABLED=0 go test -count=1 -timeout=180s ./...`、`go vet ./...` 通过；备用 SQLite 数据库测试及 `go mod verify` 通过 |
| 镜像构建上下文 | 专有 config 和临时目录哨兵未进入 builder；未带入生产配置、会话或下载数据 |
| 实际 Alpine runtime | `tests/container_smoke_test.sh` 通过，包括版本/平台、缺失旧工具、坏 CONFIG_URL、503、本地配置不覆盖、下载配置期间 SIGTERM、worker/hooks 和合成备份恢复 |
| 镜像内业务回归 | API、admin、Bot handlers、Local、config、cmd 的真实测试程序全部通过；包含 6 类 Telegram 保存端到端及媒体组、Topic、权限、取消回归。仅 Telegram RPC 为模拟输入 |
| 隔离挂载与重启 | 后台登录、CSRF、浏览/下载本地文件、拒绝目录越界及旧任务类型通过；停止/重启退出码为 0，无 OOM，旧 Cookie 失效；目录/规则/路由 Topic 与过滤条件、Userbot 合成会话、本地文件、配置及缓存哨兵保持一致 |
| 会话与回滚 | 运行镜像中的数据库恢复测试核对 Bot/Userbot 合成会话及完整停机快照；挂载演练只使用本轮独立合成数据 |
| 文档与 Windows 补测 | 更新后 Hugo 构建及 41 HTML 内部链接/锚点检查通过；补清理涉及的字符串、下载及 Bot 包在 Windows 复测通过 |

最终镜像源码核对：builder 中 267 个源文件的 SHA-256 与当前工作区一致，其中覆盖全部 250 个 Go 源文件；文档及验证材料按 `.dockerignore` 排除。runtime 程序 SHA-256 为 `6a4a51cb888caadf8992ef341b474340b30e7f4c6e4e9430180d4d19c05a56c8`。

候选 runtime：`saveany-telegram-local:runtime`，Linux/amd64，本机镜像 ID 为 `sha256:6b583fb736de14195f438a1d1b9d4fe7753249e5470af44b271098b731df739a`，`docker image inspect` 大小 101,499,956 字节，约 101.5 MB。对比本机此前完整功能镜像 586,153,318 字节，减少约 82.7%；不是注册表压缩传输大小。版本 `telegram-local`，提交标记 `bca5545-dirty`，构建时间标记 `2026-10-10-local-validation`，不是已提交发布版本或已推送的 registry digest。

挂载后台演练通过独立验证程序初始化本项目真实 config/database/storage/admin/core，主动跳过 Telegram 登录；该程序不进入候选镜像。真实主程序及入口脚本由容器 smoke 检查覆盖。没有把合成演练描述为生产 Bot 完整登录或真实媒体组联调。

证据在 WSL `/root/saveany-validation/20261010-telegram-only/evidence/`，副本在本机忽略目录 `_telegram-only-20261010/wsl-evidence/`。`linux-regression-final.log`、`container-smoke-current.log`、`runtime-*.log`、`deployment-check.log`、`docker-pull-mirror.log`、镜像标识、源码哈希与配置/文件哨兵校验均保存；初始 WSL/网络失败记录不删除。

### 生产 Bot 只读连通检查

用户确认所提供凭据属于当前生产 Bot，授权范围仅为只读连通检查。提供的用户允许列表只加入本轮私有测试配置，不修改部署配置。使用给定 SOCKS5 代理，在 WSL 主机、远程镜像及本地候选镜像内分别查询 `getMe`、`getWebhookInfo`、`getMyCommands`，最终均为 HTTP 200 且 `ok=true`。远程镜像的命令查询及候选镜像的身份查询各出现一次 TLS EOF；最多 3 次的有限重试中均在第 2 次成功，首次失败记录保留，不能据此宣称网络始终稳定。

用户指定的 `ghcr.nju.edu.cn/ghcr.io/beihehele/saveany-bot:latest` 已成功拉取。仓库摘要为 `sha256:58df2445a475e9b83f2e3e25fee9f9acceb5af08f802981b3f38e8c9642fdf7f`，Linux/amd64，大小 584,019,448 字节；镜像内版本为 `1.0.9`、提交 `6ae4475`、构建时间 `2026-07-26T14:14:32Z`。这是较早的完整功能镜像，不包含本次未提交的精简代码或管理后台；拉取与 curl 检查不能作为本次候选镜像业务验收。候选镜像仍为上文单独构建并验收的 `saveany-telegram-local:runtime`。

三组查询返回相同 Bot 身份和相同的生产默认命令菜单（25 项，仍包含待移除命令），Webhook 未配置，查询时待处理 updates 为 0。该菜单没有被更新为本次候选代码的 19 项菜单。容器仅运行版本命令或 curl，不启动 Bot 主程序，不读取/消费 updates，不发送消息，不修改菜单或 Webhook，不创建登录会话，不挂载生产数据。此次没有验证 MTProto 的 `app_id` / `app_hash` 登录，也没有验收真实 `/watch`、`/copy`、媒体组、Topic 或文件保存；这些仍需隔离环境。

脱敏证据保存在 WSL `/root/saveany-validation/20261010-telegram-readonly/evidence/`，并复制到本机忽略目录 `tmp-architecture-validation-20261009/_telegram-readonly-20261010/evidence/`。收尾核对两份目录的 28 个证据文件均不含 token、app_hash 或代理密码；镜像标识保持一致，本轮只读检查容器全部清理，没有创建会话或数据目录。WSL 私有配置权限确认是 `0600`；检查完成后删除本轮拥有的 Windows/WSL 两份临时凭据配置，脱敏证据保留。配置与凭据不写入审查文档、镜像或版本控制。

### 补充 Userbot 配置与离线会话核对

用户随后提供 `usersession.db` 及启用 Userbot 的配置。另建 WSL 私有目录 `/root/saveany-validation/20261010-userbot-config/`，将此前提供的 Bot、代理、用户允许列表与 `[telegram.userbot] enable = true / session = "data/usersession.db"` 合并到独立 `config.toml`，会话副本放在该目录的 `data/usersession.db`。仅增加独立 `local-test` 存储，API/Admin 关闭；这不是生产配置迁移方案，也没有修改实际部署配置。相对会话路径以该私有目录作为工作目录使用，不受配置文件位置自动定位。

离线以 SQLite `mode=ro&immutable=1` 和 `query_only` 打开会话副本：完整性检查为 `ok`，`sessions`/`peers` 表符合当前 gotgproto 格式，唯一会话记录和 JSON 载荷版本均为 1，认证密钥长度及其 ID 校验一致。复制前后原文件 SHA-256 一致，副本与原件一致，没有旁路 WAL/journal 文件；没有输出密钥、个人会话内容或 peer 列表。私有目录权限为 `0700`，完整测试配置和会话副本为 `0600`，不在项目目录或镜像构建上下文内。

这一步仅准备配置及核对离线格式，不调用 Bot/Userbot 初始化或自动迁移，不启动 MTProto，不消费 updates，不执行监听、复制或转发。用户尚未扩大生产只读授权范围；会话是否仍被 Telegram 服务端授权、`app_id` / `app_hash` 登录及真实业务行为仍未验收。私有配置及副本为后续验收保留，脱敏结果为上述证据目录中的 `userbot-offline-config.json`。

### 保留链路的既有限制

API 源链接解析仍沿用既有 Telegram 客户端上下文；部分取消息/取相册 RPC 未绑定 HTTP 请求取消。HTTP 断连后这些准备 RPC 可能继续到客户端超时，但 factory 入队前会检查请求取消，不会据此创建一个新任务；已接受任务的取消仍使用服务队列控制。本次未重写该来源链路，模拟 source adapter 的取消测试不能替代真实 RPC 取消验收。后续应独立评估这项取消边界，并覆盖 Bot/Userbot 回退及媒体组行为。

## 发布评估与下一步

本次精简代码和 WSL 隔离自动验收已具备合入基础；没有发现尚未修复的新引入阻塞项。真实 Telegram 及生产配置迁移尚未验收，当前不能宣称完成生产发布验收。配置/数据/会话/下载目录未改动，Git HEAD 与暂存区保持原状。管理后台预览使用本次专有的合成数据目录和假 token，不是生产实例。

1. 本轮 WSL 构建、候选 runtime 回归、备份恢复、重启持久化与 SIGTERM/hooks 已完成。若目标部署机器仍无法直连镜像仓库，先验证可访问的镜像来源和固定摘要，或导入本机已验收镜像；本次未修改宿主机代理/网络策略。
2. 停止并备份部署后，在配置副本检查 Local 名称、用户允许列表、默认目录/规则与路由。按中英文迁移说明核对；发现旧引用时修正副本，不删除生产数据库或会话。
3. 有隔离环境后验收 `/watch`、`/copy`、相册转发的边界/顺序/去重、Topic、过滤、caption、单/批本地保存及取消。真实媒体组验收记录与模拟 RPC 自动测试分开保存。
4. 上述验收通过后再进行人工生产切换，固定候选镜像版本/摘要并保留旧镜像、对应配置和恢复材料。本轮不自动提交或发布。

详细测试、源文件快照与失败日志位于本机忽略目录 `tmp-architecture-validation-20261009/_telegram-only-20261010/`；这些验证材料不进入镜像构建上下文。

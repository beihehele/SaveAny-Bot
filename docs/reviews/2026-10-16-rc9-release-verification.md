# rc.9 发布与 NAS 验收

2026-10-16。用户授权将已验收的 ShardReel 接入和媒体组 caption 目录功能发布为新候选，并将 NAS 从开发镜像切换到可重新拉取的发布镜像。默认保存位置保持 `local`。

## 发布身份与质量检查

- 候选：[v2.0.0-rc.9](https://github.com/beihehele/SaveAny-Bot/releases/tag/v2.0.0-rc.9)，GitHub 预发布。
- 功能提交：`dbfe8c066edf54029a9f6bd3af218d7da85b049e`。
- 发布提交：`64d685139eb808931507eb77a85778c7e2afcf22`，仅比功能提交增加发布说明更新。候选标签保持指向该提交。
- [Quality 38838703353](https://github.com/beihehele/SaveAny-Bot/actions/runs/38838703353)、[Build Release 38838780956](https://github.com/beihehele/SaveAny-Bot/actions/runs/38838780956)、[Docker 38838780969](https://github.com/beihehele/SaveAny-Bot/actions/runs/38838780969) 全部成功。各发布流程的质量门禁也通过。
- 检查包括全项目测试、vet、竞态、网页脚本与交互回归、SQLite 变体、Linux/Windows 构建、可达漏洞及实际容器运行检查。

首次推送时 GitHub 因 Actions 使用规模暂停了该 Fork。维护者重新启用后，手动触发分支 Quality，并在确认尚无 rc.9 发布和构建记录的前提下，重新推送同一个 annotated tag 对象，补触发发布；未改变标签目标。

## 实际产物校验

实际下载 Linux/amd64 tar.gz 和 Windows/amd64 zip，校验文件长度、GitHub digest 和随包 SHA256；两包均包含配置示例、许可证、README 和发布说明，不含会话数据库。Windows 本机与 Linux WSL 离线执行包内程序，版本与完整发布提交一致。WSL 未启动 Bot 服务。

官方镜像为 `ghcr.io/beihehele/saveany-bot:2.0.0-rc.9`，Linux/amd64：

| 项目 | SHA-256 |
| --- | --- |
| 镜像索引 | `c3c5d735f169744686f29357d8be3b02433c93972d9151102187b306902abdb3` |
| Linux/amd64 manifest | `2c942de09b6806212e9a842f0d949b427a966dc372d884835633e8671fc3830b` |
| 镜像配置 / image ID | `33e2541e5d39d097c17d9ec411e4ee9d413002a79788ed1a5f0706214882257e` |

NAS 从 `ghcr.nju.edu.cn/ghcr.io/beihehele/saveany-bot@sha256:c3c5d735f169744686f29357d8be3b02433c93972d9151102187b306902abdb3` 拉取；配置、平台和全部 layer diff IDs 与官方 registry 核对一致，无网络执行容器 `version` 确认 `2.0.0-rc.9`、`64d6851`、Go 1.26.9。

GitHub 最新正式版本保持 `v1.0.9`，Docker `latest` 保持 `sha256:58df2445a475e9b83f2e3e25fee9f9acceb5af08f802981b3f38e8c9642fdf7f`；未提升稳定别名。

## NAS 部署与验收

停服前确认任务空闲。停止 SaveAny-Bot 后，备份配置、两个 Compose 文件、业务与会话数据库、ShardReel 待上传目录、凭据文件和运维脚本。备份逐文件比对停止状态，提取数据库后独立执行完整性检查，三个 SQLite 均为 `ok`。

备份为 `backups/state-before-rc9-20261016T022024Z.tar.gz`，附 `.sha256` 文件；SHA-256 为 `8545c488c66c248f2873366e41955bcceccd2744894dad04d8ef0be4f9c0320b`。下载媒体不包含在状态备份中。保留原开发镜像作为回退程序。

只在 Compose 覆盖文件中更换镜像并将拉取策略设为 `missing`。有效 Compose 比较确认其余配置一致，原始 Compose、应用配置和全部挂载保持不变；ShardReel 容器身份和启动时间也未变化。

- 候选启动、容器重启、管理员登录均通过；Bot/Userbot 就绪，持久上传历史保留。
- 三个网页静态资源的响应哈希与发布提交中的文件一致；ShardReel 连接检查和独立健康检查通过。
- 用户用常用 Telegram 客户端发送 `/help`，确认正常收到包含 `/watch`、`/copy` 的回复。
- 使用既有私有验收频道中已知内容的 78 字节合成文件，分别通过后台创建 `local` 和 ShardReel 保存任务。两任务均完成，网页目录可见，完整下载后的 SHA-256 与 Telegram 源文件的已知值一致。
- ShardReel 新任务的回执成功、对象就绪，大小和哈希一致，无重试，本地副本清理成功。两个合成验收文件保留在独立的 `acceptance-rc9/20261016T022201Z` 目录下。
- 用户偏好未改变，默认存储仍为 `local`；最终待上传目录无残留文件，运行日志无错误或警告。

## 原有真实任务与验收范围

发布前，同运行代码的开发镜像完成三次真实 Telegram 保存，共七个文件、1,656,141,123 字节；当时回执、远端大小、SHA-256、目录成员和本地清理均核对通过，未完整重新下载这些真实文件。

升级后三条历史任务和七个文件状态均保留。六个现存远端文件的大小和 SHA-256 再次核对一致。一份约 95 MiB 的单文件原路径返回不存在：上传回执对象 ID 与 ShardReel 原生删除接口的成功日志对应，删除发生于北京时间 2026-10-16 07:52:57，早于本次升级；用户也确认此前做过文件操作。保留其已删除状态，未重新下载或恢复。

本次正式候选的在线验收覆盖命令接收、双存储保存、完整小文件下载、本地清理、历史保留和容器重启；未在 NAS 重新进行全部照片/视频组的 `/watch`、`/copy` Topic 转发、异常注入和全机重启。自动回归及既有真实媒体验收属于各自记录的范围。备份提取校验不等于完整灾难恢复演练；真实管理员撤权仍缺少第二账号测试条件。

候选发布和本次 NAS 升级验收通过，继续保持预发布状态。私有配置、会话及辅助工具未进入提交；本地原始证据保存在忽略的 `tmp-publish-rc9-20261016/`。

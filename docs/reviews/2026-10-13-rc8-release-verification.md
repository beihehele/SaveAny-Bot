# rc.8 合入、发布与产物核验

2026-10-13。用户批准发布标准复核后的改动合入 `dev` 并发布新候选。

## 发布身份

- 候选：[v2.0.0-rc.8](https://github.com/beihehele/SaveAny-Bot/releases/tag/v2.0.0-rc.8)。GitHub 标记为预发布，不是草稿。
- 代码提交：`32e995d6d2483f5306e9fdc006c99631802a3748`。远端 `dev` 与 annotated tag 的目标提交核对一致。
- 修复与审查依据：[个人使用范围项目审查](2026-10-13-personal-project-audit.md)。本候选包含消息读取、媒体组目录规则、保存表单和日志修复，以及历史配置/API/索引兼容与文档残留清理。
- 官方镜像：`ghcr.io/beihehele/saveany-bot:2.0.0-rc.8`，Linux/amd64。
- 官方镜像索引摘要：`sha256:12219d1d8920ee4dda82f8319630c58ce9d0292464e240a2989a026bfececd0e`。
- Linux/amd64 manifest 摘要：`sha256:50379caed2839259436de630fc81cdc7bd26807aee1403bd1f606d5c4053d38c`。
- 镜像配置摘要：`sha256:1a9741d525b7ca9dbd2d9bc37224135acafdcdde5ea73c3c4a7d6d1173378bce`。

## CI 与发布包

| 流水线 | 结果 |
| --- | --- |
| [Quality 38395125523](https://github.com/beihehele/SaveAny-Bot/actions/runs/38395125523) | 成功 |
| [Build Release 38395125833](https://github.com/beihehele/SaveAny-Bot/actions/runs/38395125833) | 成功 |
| [Docker 38395125924](https://github.com/beihehele/SaveAny-Bot/actions/runs/38395125924) | 成功 |

该代码提交的全项目测试、vet、全项目竞态、Node 检查、SQLite 变体、支持平台构建、可达漏洞检查和实际容器回归均通过。二进制与镜像发布流水线各自的质量门禁也通过。

实际下载 Linux/amd64 tar.gz 与 Windows/amd64 zip，核对文件大小、GitHub 提供的 digest 和随包 SHA256。两个包均包含许可证、README、配置示例与发布说明，未包含会话数据库。Windows 本机与 Linux WSL 离线执行 `version`，版本为 `2.0.0-rc.8`，提交为完整 `32e995d6d2483f5306e9fdc006c99631802a3748`。

WSL 通过 `ghcr.nju.edu.cn/ghcr.io/beihehele/saveany-bot@sha256:12219d1d8920ee4dda82f8319630c58ce9d0292464e240a2989a026bfececd0e` 拉取镜像。与官方 registry 的索引、平台 manifest、配置及本地全部 layer diff IDs 核对一致；无网络执行容器 `version` 确认版本与短提交 `32e995d`。下载包直连较慢；辅助代理下载尝试遇到 TLS 错误，最终直连下载成功并完成独立校验，不属于产品运行错误。

GitHub 最新正式版本仍为 `v1.0.9`。Docker `latest` 前后均为 `sha256:58df2445a475e9b83f2e3e25fee9f9acceb5af08f802981b3f38e8c9642fdf7f`；未提升稳定别名。

## 真实验收与环境范围

发布前新源码镜像已完成真实 Telegram 照片/视频保存、监听及复制回归，详见项目审查报告。提交内构建源码与最终审查快照逐文件一致，仅允许 Git 的 CRLF/LF 规范化。此前验收镜像与最终快照的差异仅在两个测试文件，运行代码、资源和构建配置一致。

本轮正式发布产物验证为离线版本/提交、包内容、摘要与 CI 检查；没有把发布前源码镜像的真实验收冒称为正式 rc.8 产物的在线实测。WSL 接收消息的实例仍为 rc.7，本轮未切换。只读状态检查确认后台、认证、容器和本地存储正常，任务队列空闲、无临时监听。最终飞牛 NAS 部署、缺少第二账号的真实管理员撤权验证仍未进行，不增加历史兼容或新的防御层。

原始 Userbot 会话和用户未跟踪的网盘设计草稿保持不变。私有配置、会话、下载包及辅助验证工具均不进入提交。

## 结论与证据

rc.8 已合入 `dev`、推送并完成候选发布，流水线和实际发布产物核验通过。可以进入候选部署验收；此次结果不自动提升稳定版本。

本地证据位于忽略的 `tmp-publish-rc8-20261013/`，WSL 镜像与二进制证据在 `/root/saveany-validation/20261013-published-rc8/`。正式候选标签保持指向上述代码提交，后续发布记录提交不改变候选身份。

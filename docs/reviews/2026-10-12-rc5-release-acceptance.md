# rc.5 发布与真实 Telegram 验收

本报告接续 [集中修复记录](2026-10-12-backlog-remediation.md)。代码提交为 `634b6533dc0cdaf1178df95f48fbe73bf45f4f2f`，分支 `dev`，发布标签 `v2.0.0-rc.5`。本报告的后续文档提交不改变标签指向，也不重新打包不同源码。

## 发布产物

- [分支 Quality](https://github.com/beihehele/SaveAny-Bot/actions/runs/38256014577)、[Release](https://github.com/beihehele/SaveAny-Bot/actions/runs/38257401720)、[Docker](https://github.com/beihehele/SaveAny-Bot/actions/runs/38257401741) 全部成功，均对应上述代码提交。
- [GitHub Release](https://github.com/beihehele/SaveAny-Bot/releases/tag/v2.0.0-rc.5) 为预发布；稳定版 latest 仍为 `v1.0.9`。Docker 仅发布候选版本/提交标签，未覆盖稳定 latest。
- Linux/amd64 tar.gz 和 Windows/amd64 zip 的发布 SHA256、包内配置示例、发布说明均已核对；两平台发布二进制的离线版本与提交身份一致。
- 官方 OCI index 为 `sha256:d323b5f9048ae93e7c2fd7ac5f2be0dfb723ca615ead59e301d8b44649f6e40b`；Linux/amd64 manifest 为 `sha256:156150c807fb1e093358f670cff1a4a20b04a1f9566b9e241c98075508fc8184`；配置为 `sha256:856491c3d40e05bb91c2e58293c9336eb39d126732c8257c924c01b596154384`。

## WSL 部署来源及保护

WSL Ubuntu-24.04 是验收实例，尚未迁移飞牛 NAS。南京镜像源的相同固定摘要拉取在有界尝试中超时，不能计作镜像源拉取通过。改从官方 GHCR 经现有配置代理获取同一摘要的镜像归档，核对归档配置及全部六层的解压后摘要、Docker 加载后的文件层/运行配置和二进制版本。没有修改共享代理或 Docker 代理配置。

归档 SHA256 为 `7c2d2c87937ef56ea9d55a682b92973689a55ba8827efbfdf67f895dd467fe5f`。归档经 `docker load` 导入时重新生成 manifest，加载后 image ID 为 `sha256:341683276a8eb91d0d2680db82613bdef933a1d578a1e1f368039b78123cd15e`；它不是官方 OCI index/manifest 摘要，不能混用。部署引用 `saveany-rc5-verified:634b653`，身份及官方来源记录在实例的 `deployment.json`；没有伪造南京镜像源 RepoDigest。

升级前确认队列空闲、无监听，停止唯一实例完成一致性备份，再以禁止拉取/禁止重建方式启动已核验镜像。完整备份 `/opt/saveany-bot/backups/pre-rc5-20261012-143600.tar.gz`，SHA256 `a50e0c5d5d3f65da8c67958e067f7ae734938eee1be7cf3c847d7df026e4a067`，含配置、compose、部署记录、数据库/session、下载、缓存和私有运维文件；回滚路径保留。

部署后后台版本 `2.0.0-rc.5` / `634b653`、健康检查、匿名拦截、管理员登录/退出、本地存储浏览、19 条 Telegram 命令菜单同步均通过。API 关闭，Userbot 开启；后台端口仅绑定本机 `18082`。原始 Windows session 与用户未跟踪草稿的哈希保持。

## 真实验收结果

验收仅使用此前获授权的私有测试源、目标论坛及稳定 Topic ID 2，没有向其他用户或群发消息。通过后台管理接口驱动真实 Bot/Userbot 任务和路由；这与 Telegram 命令输入入口的验收分别记录。

已确认照片/视频两组各两成员的本地监听保存和 Topic 转发；文件哈希与 Telegram 原始下载一致，顺序、组身份、caption、粗体偏移、来源链接及 DropAuthor 均符合预期。四个成员链接给出 `ready/duplicate/ready/duplicate`，实际保存四个文件；`?single` 只保存指定成员。历史复制、照片/视频过滤复制各按逻辑项计数为 1，均核对实际完整两成员组。运行中取消保留 5 项确认成功、7 项待处理，终态 cancelled 且队列空闲；随后照片过滤复制成功，完整组保持。各阶段配置、偏好与候选进程未改变。

辅助客户端首次上传 RPC 失败，尚未发送测试组，临时路由已清理；改为复用此前合成测试素材创建新测试组完成上述检查。其后读取目标消息/源历史出现 DC5 EOF 与连接重试失败，保留原始证据，不重复已执行的发送。验收工具使用 session 保存的服务器地址列表进行对照；地址列表对照仍有失败；随后仅对临时工具使用非 ThisPortOnly 主 DC 的 80 端口，成功完成只读对照。首个跨页标记发送结果未确认，但独立源历史读到该文本，证明不能据 RPC 失败推断未发送。保留该证据，以新的独立标记开展跨页验收，未重复旧标记发送；源历史用于观察的备用端口不修改生产配置，暂不宣称外部链路异常根因已解决。

后台真实实例补充验证错误 CSRF 拦截、无效目录与静默设置混合 patch 的原子回滚、相同默认存储重选保持偏好，以及停止后重建获得新 ID、旧 ID 停止请求无法删除新监听；临时路由已移除。真实服务消息跨页验收通过：私有源中按间隔发送 100 条合成文本并产生一个置顶服务事件，原始第一页长 100 且含一条服务消息，标记文本在下一页。通过没有正向搜索种子的 `msgre:!(!marker)` 强制历史扫描，只复制一个标记文本至指定 Topic，来源链接对应标记 ID；没有复制噪声文本。测试置顶已移除。用户常用客户端手动 `/help` 收到含 `/watch`、`/copy` 的正常回复，当前版本命令接收入口通过；没有把后台入口自动等同于全部原生命令交互通过。未人为制造共享代理故障或限流。连续真实扫描超时恢复、真实交错到达、超窗口或服务端缺失成员的完整性，以及飞牛 NAS 迁移仍不在本次已验收范围。本次结果支持继续使用 rc.5 作为候选验收版本；保持 RC 身份，尚不直接晋升正式版。

最终复核版本和提交一致，容器 healthy、重启 0、运行/排队 0、监听 0，候选进程保持，后台登录与存储浏览正常，命令菜单仍为 19 条；临时 session 副本与临时监听已清理，测试置顶已移除，WSL 保活继续。原始 session、用户未跟踪草稿均未修改，升级备份保留。辅助 `/help` 历史读取仍曾失败，单独保留失败与用户手动回复确认，不据此宣称辅助客户端已经稳定。

## 私有证据

本机忽略目录 `tmp-publish-rc5-20261012/` 保留提交清单、CI 状态、发布包校验与验收脚本。WSL `/root/saveany-validation/20261012-published-rc5/evidence/` 保存镜像来源核验；`/opt/saveany-bot/secrets/acceptance-rc5/` 保存升级、真实业务、辅助失败和清理记录，权限受限，不纳入 Git。临时 session 副本仅用于验收，结束后删除，不删除原 Userbot session。

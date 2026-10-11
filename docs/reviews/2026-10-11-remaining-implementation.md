# 遗留项实施与候选验收安排（2026-10-11）

本报告接续 [上一轮评估](2026-10-11-remaining-work.md)。清理批次为 `7db9143`；功能批次 `237b53c` 包含任务字段清理、扫描统计与恢复回归。两批已推送 dev；候选 `v2.0.0-rc.4` 对应完整源码提交 `237b53c8489af8a66678a68b991d69b23ce4e44f`。本报告的后续文档更新不改变候选运行代码或标签。

## 本轮实施

| 项目 | 已实施内容 | Review 边界 |
| --- | --- | --- |
| 旧注释与模板残留 | 删除 MessageItem / IterMessages 注释草稿，共 91 行（含空行）；删除 Bug 模板的上游 assignee。 | 草稿全部不参与编译；实际取消息、媒体组逻辑与 ID 转换保留。 |
| 运维说明 | 增加中英文启动、检查、停机备份、升级和回退模板，并从安装文档链接。 | 模板需按实际持久路径调整；WSL 登录自启动暂缓，不代替最终服务器方案。 |
| 冗余任务字段 | 删除单文件/批量任务未使用的 context 字段与参数、copy 未使用的进度字段；同步调用方。 | 执行仍使用 Execute(ctx) 与队列保存的 context；实际进度追踪、hooks、客户端传递和取消保留。 |
| 扫描恢复回归 | 同一客户端连续两次超时后第三次成功；分别覆盖 history/search、首页/后页超时与无部分结果。 | 使用模拟 RPC。负对照破坏共享 Raw 客户端后断言失败，证明回归能发现客户端污染。不是完整真实网络故障验收。 |
| 扫描诊断 | 后台显示 history/search 的页数、成功/失败、超时/取消、最近/最大耗时。 | 固定两个内存计数项，自进程启动累计；不保存内容、聊天 ID 或原始错误，不增加 RPC。失败包含超时/取消；页成功不代表转发成功。 |

媒体组聚合、整组接纳、去重、顺序、Topic、过滤、存储实现和队列 worker 没有重构。监听入口仅删除用于写入闲置字段的临时变量，真正接纳任务的 context 保留；回归检查队列包装任务携带原客户端。

本轮 Windows 常规测试、vet、全量竞态、SQLite 变体与构建通过；WSL 全量测试、vet、SQLite 变体、入口契约、12 个相关包的离线 Alpine 回归与容器冒烟通过。中英文隔离后台页面核对了扫描展示；共享客户端污染负对照触发预期断言失败。当前运行代码的 [Quality](https://github.com/beihehele/SaveAny-Bot/actions/runs/38133859492)、[Release](https://github.com/beihehele/SaveAny-Bot/actions/runs/38134316241) 和 [Docker](https://github.com/beihehele/SaveAny-Bot/actions/runs/38134316253) 均成功。证据目录为 `tmp-month-review-20261011/remaining-implementation/`，不随发布上传。

## 候选发布结果与剩余门槛

1. **当前运行代码验证与发布完成**：源码冻结一致，本地 Windows/WSL/Alpine 检查与上述三条 CI 通过。候选为 [v2.0.0-rc.4](https://github.com/beihehele/SaveAny-Bot/releases/tag/v2.0.0-rc.4)，Linux/Windows amd64 归档校验和、运行版本和提交核对通过；未更新稳定 latest。
2. **镜像核对完成**：南京镜像源固定参考为 `ghcr.nju.edu.cn/ghcr.io/beihehele/saveany-bot@sha256:5fd96563a63568ea1a9329198335264e36896ff73591bc42de59c41ddaa670ea`。官方 OCI 索引、平台与 config 摘要已核对，镜像根文件系统及全部运行配置匹配。当前 Docker 的 image ID 为索引摘要，不应误当作 config 摘要。
3. **WSL 候选升级与真实功能验收完成**：停机备份包含全部确认的持久挂载与部署私有文件，备份校验通过。rc.4 真实核对了后台混合/重复/失败链接、全失败不接纳、single、Bot 独立提示和终态来源；指定媒体组本地保存以文件哈希核对，watch 单成员过滤整组命中，Topic 转发、顺序、富文本与来源链接保留。history/search copy、运行中取消及取消后再 copy 成功；真实扫描统计两种模式均有成功页。测试期间候选未重启，临时路由和辅助会话副本已清理，保存偏好恢复，配置与升级前备份一致；最终队列为空，容器健康，API 仍关闭。WSL 是本机验收环境。
4. **严格真实超时恢复仍有边界**：rc.3 已观察到同进程连续两次真实扫描超时无部分发送；rc.4 模拟 RPC 覆盖同客户端“失败、失败、成功”。恢复默认期限并升级后的实际成功是部署恢复证据，不能冒充同进程真实完整序列。没有可控隔离故障时保留未验收，不增加生产故障开关。
5. **最终 NAS 迁移尚未执行**：目标已确认飞牛 NAS、Intel x64、已有 Docker，对应 linux/amd64；连接地址/方式、Compose 可用性、挂载路径、权限、备份目录、代理可达性和后台访问范围仍需核实。需要在目标环境补重启恢复、备份恢复及旧版本回退，才评估最终生产发布。

本轮最终证据位于上述忽略目录的 `local-validation.json`、`runs-branch.json`、`runs-tag.json`、`release-assets-validation.json`、`mirror-verification.json`、`wsl-deployment.json`、`live-verification.json`、`acceptance-summary.json`。私有 fixture 与会话不随提交或产物发布；报告中的通过范围只限于记录场景。

## 后续项目

| 项目 | 状态与推进条件 |
| --- | --- |
| 后台管理闭环 | 监听路由、保存偏好仍只读；copy 向导、路由编辑、Topic 选择与命名/过滤预览应独立设计、验证，不并入本次候选。 |
| API 终态数量预算、跨重启历史 | 当前进程内记录；API 终态只有期限预算，不能套用 Bot 的 1000 条额度。按实际历史需求单独实施。 |
| 媒体组更多到达顺序与照片/视频覆盖 | 按启用场景补真实验收。已有合成文件验收不能代表所有格式，真实 FLOOD_WAIT 未覆盖，不刻意制造限流。 |
| 辅助客户端偶发无回复 | 用户常用客户端正常；再现时关联脱敏事件与 RPC 时序，禁止盲目重复有副作用命令。 |
| WSL 长期保活/自动启动 | WSL 为本机验收环境，已有状态/停止/备份脚本；长期启动策略随最终 NAS 部署核实。 |
| 用户草稿与历史证据 | 保持用户未跟踪草稿原样；历史报告保存当时结论，当前待办以本报告和候选最终验收为准。 |

运维入口见 [中文部署操作](../content/zh/deployment/operations.md) 与 [英文部署操作](../content/en/deployment/operations.md)。候选升级成功不等于目标 NAS 迁移、恢复与回退已验收。

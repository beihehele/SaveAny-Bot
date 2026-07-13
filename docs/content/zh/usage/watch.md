---
title: "监听聊天"
weight: 4
---

# 监听聊天

{{< hint warning >}}
该功能需开启 UserBot 集成.
{{< /hint >}}

监听指定聊天的媒体消息。`target=0`（或省略）时自动保存到默认存储；`target` 为其他频道/群 ID 时由 UserBot 转发（不下载）。

## 监听聊天

```
/watch <source_id> [target_id] [filter]
```

示例:

```
/watch -1002229835658
/watch -1002229835658 0 msgre:.*hello.*
/watch -1002229835658 -1003333444555
/watch -1002229835658 -1003333444555 msgre:.*hello.*
```

## 列出监听

```
/lswatch
```

输出格式:

```
[id] <source名> -> <target名> [filter]
```

`target` 为 `0` 时显示为「本地」。可用 `/lschannel`、`/lsgroup` 查看名称与 ID 对应关系。

## 列出频道 / 群组

```
/lschannel
/lsgroup
```

输出格式:

```
<名称> -> <id>
```

## 取消监听

```
/unwatch <id>
```

`id` 为 `/lswatch` 返回的自增主键。

## 过滤器

### msgre

正则匹配消息文本, 例如:

```
/watch -1002229835658 msgre:.*hello.*
```

## 转发说明

- 转发由 **UserBot 个人账号** 执行，Bot 仅负责管理命令
- 使用无来源复制（`DropAuthor`），目标侧为独立副本，源消息删除不影响目标
- 相册/9 宫格媒体会聚合后一次转发，避免被打散
- 源聊天需可读，目标聊天需有发消息权限

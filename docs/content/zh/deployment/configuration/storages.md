---
title: "存储端配置"
---

# 本地存储

目前只支持 `local`，可以配置多个不同名称、不同根目录的本地存储。

```toml
[[storages]]
name = "本机1"
type = "local"
enable = true
base_path = "./downloads"
```

名称唯一，启用时 `base_path` 必填。容器中使用 `/app/downloads` 对应的挂载目录。用户 `storages` 为允许/排除的名称列表，`blacklist` 控制列表含义。

用 `/storage` 选择默认存储，`/dir` 登记常用相对目录；`/rule` 可以选择其中任一本地存储。已保存文件不因删除目录登记而删除。网页提供目录浏览和文件下载。

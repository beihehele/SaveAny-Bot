---
title: "yt-dlp 视频下载"
weight: 7
---

# yt-dlp 视频下载

{{< hint warning >}}
该功能需要在系统中安装 yt-dlp 命令行工具.
{{< /hint >}}

使用 `/ytdlp` 命令可以下载支持的视频网站的视频和音频, 支持 YouTube、Bilibili、Twitter 等 1000+ 个网站.

```bash
/ytdlp <url1> [url2] [flags...]
```

示例:

```bash
# 基本下载
/ytdlp https://www.youtube.com/watch?v=dQw4w9WgXcQ

# 下载多个视频
/ytdlp https://www.youtube.com/watch?v=video1 https://www.youtube.com/watch?v=video2

# 使用自定义参数
/ytdlp https://www.youtube.com/watch?v=dQw4w9WgXcQ -f best
/ytdlp https://www.youtube.com/watch?v=dQw4w9WgXcQ --extract-audio --audio-format mp3
```

常用参数:

- `-f <format>`: 指定下载格式 (如 `best`, `worst`, `bestvideo+bestaudio`)
- `--extract-audio`: 提取音频
- `--audio-format <format>`: 音频格式 (如 `mp3`, `m4a`, `wav`)
- `--write-sub`: 下载字幕
- `--write-thumbnail`: 下载缩略图

自定义 `-o` / `--output` 和 `-P` / `--paths` 使用相对路径, 由 Bot 放在该任务的临时目录内. 支持子目录和 `subtitle:` 等类型前缀; 绝对路径、向上越界的路径和标准输出 `-` 会报错. 下载后的文件仍按文件名平铺保存到所选存储目录; 如果不同子目录中的文件会保存为同一个文件名, 任务会在上传前报错, 避免相互覆盖. 该规则同样适用于 API 的 `flags`.

原有格式默认行为保持不变: 不传自定义参数时应用配置中的格式设置; 传入任意自定义参数时, 由这些参数控制格式和文件名限制.

更多参数请参考 [yt-dlp 文档](https://github.com/yt-dlp/yt-dlp#usage-and-options).

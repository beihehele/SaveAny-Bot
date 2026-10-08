---
title: "yt-dlp Video Download"
weight: 7
---

# yt-dlp Video Download

{{< hint warning >}}
This feature requires the yt-dlp command-line tool installed on your system.
{{< /hint >}}

Use the `/ytdlp` command to download videos and audio from supported video websites, including YouTube, Bilibili, Twitter, and 1000+ other sites.

```bash
/ytdlp <url1> [url2] [flags...]
```

Examples:

```bash
# Basic download
/ytdlp https://www.youtube.com/watch?v=dQw4w9WgXcQ

# Download multiple videos
/ytdlp https://www.youtube.com/watch?v=video1 https://www.youtube.com/watch?v=video2

# Use custom parameters
/ytdlp https://www.youtube.com/watch?v=dQw4w9WgXcQ -f best
/ytdlp https://www.youtube.com/watch?v=dQw4w9WgXcQ --extract-audio --audio-format mp3
```

Common parameters:

- `-f <format>`: Specify download format (e.g., `best`, `worst`, `bestvideo+bestaudio`)
- `--extract-audio`: Extract audio
- `--audio-format <format>`: Audio format (e.g., `mp3`, `m4a`, `wav`)
- `--write-sub`: Download subtitles
- `--write-thumbnail`: Download thumbnail

Custom `-o` / `--output` and `-P` / `--paths` values must be relative paths. The bot roots them in the task's temporary directory, supporting subdirectories and type prefixes such as `subtitle:`. Absolute paths, paths escaping that directory, and stdout `-` are rejected. Files are still saved by filename directly in the selected storage directory. If files from different subdirectories would have the same storage filename, the task fails before uploading to prevent overwrites. This also applies to API `flags`.

Format defaults remain unchanged: configuration applies when no custom flags are supplied; any custom flags give the caller control over format and filename restrictions.

For more parameters, see [yt-dlp documentation](https://github.com/yt-dlp/yt-dlp#usage-and-options).

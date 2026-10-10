---
title: "Storage Configuration"
---

# Local storage

Only `local` is supported. Configure multiple named roots when needed.

```toml
[[storages]]
name = "archive"
type = "local"
enable = true
base_path = "./downloads"
```

Names must be unique and enabled roots require `base_path`. Mount `/app/downloads` in Docker. User `storages` lists allowed or excluded names according to `blacklist`.

Use `/storage` for the default root, `/dir` for saved directory shortcuts and `/rule` for routing to local roots. Deleting a directory shortcut does not delete saved files. The console can browse and download local files.

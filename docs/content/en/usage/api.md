---
title: "HTTP API"
weight: 20
---

# HTTP API

The optional API is independent of the admin console. Enable `[api]` with a listener and nonempty token. Every request, including `/health`, needs `Authorization: Bearer <token>`.

Endpoints: `POST /api/v1/tasks`, `GET /api/v1/tasks`, `GET /api/v1/tasks/{id}`, `DELETE /api/v1/tasks/{id}`, `GET /api/v1/storages`, `GET /api/v1/task-types`, and `GET /health`.

```json
{"type":"tgfiles","storage":"archive","path":"albums","params":{"message_links":["https://t.me/c/123456789/123"]}}
```

`path` is a destination directory for both single files and albums. Bot/Userbot must have source access. Album links expand to the whole group unless `?single` is present. The API does not create watch/copy routes.

An optional `webhook` receives terminal events. `result_policy` is unsupported for Telegram saves. Task records live in memory and disappear on restart; terminal records are retained for about 24 hours. Accepted tasks survive the originating HTTP request ending.


After a batch executes, query responses and normal terminal webhooks may include `result_summary` counts: total/pending/running/succeeded/failed/cancelled/interrupted. Failed includes files skipped by storage policy. Counts describe file outcomes without changing task status or triggering retries. Tasks that never execute or cannot provide valid counts omit the field. An early cancellation webhook can lack final counts; later queries can expose them without another notification. Nonempty `result_policy` is explicitly rejected.

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

Select a configured local or ShardReel storage. `path` is the base destination: ordinary files go directly under it, while albums use a cleaned, truncated caption subdirectory. Equal caption names share a directory; see [storage configuration](../../deployment/configuration/storages/#shardreel). Bot/Userbot must have source access. Album links expand to the whole group unless `?single` is present. The API does not create watch/copy routes.

Overlapping links within one submission are deduplicated by chat and message ID; identical message IDs in different chats stay independent. `?single` does not expand an album, while another link without it can still select that whole album. Deduplication does not affect later independent saves. Source queries honor HTTP request cancellation, discard partial selections on cancellation and preserve shared Bot/Userbot clients.

An optional `webhook` receives terminal events. Requests and task parameters accept only the documented fields and one JSON value. Ordinary task records live in memory and disappear on restart. Web/API submissions share a terminal history budget of 1000 records for up to about 24 hours; the least recently updated records are evicted first. Evicted task details return 404. Queued and executing tasks, including cancellation cleanup, are exempt from both limits. History eviction does not suppress terminal webhook delivery or retries. Accepted tasks survive the originating HTTP request ending.

ShardReel tasks and file results persist in SQLite, including Bot-originated uploads in task lists. Responses include `upload_files`, `can_retry` when applicable and the `cancelling` state. Download completion only queues the upload; the final result and webhook wait for confirmed remote ingest. The task deletion endpoint requests cancellation; the admin console can retry retained files or discard them. Clean terminal history lasts 7 days, up to 1000 tasks; failed, unfinished or unclean records remain. Input-selection reports and webhook registrations remain in memory, so a restart does not replay a lost webhook. Upload recovery is independent of them.


After a batch executes, query responses and normal terminal webhooks may include `result_summary` counts: total/pending/running/succeeded/failed/cancelled/interrupted. Failed includes files skipped by storage policy. Counts describe file outcomes without changing task status or triggering retries. Tasks that never execute or cannot provide valid counts omit the field. An early cancellation webhook can lack final counts; later queries can expose them without another notification.

Creation responses and task queries include `input_results`: one-based `index`, `status` (ready/duplicate/failed/partial), a selection-stage `code`, and `new_files`. Valid selections can still be queued alongside rejected or duplicate links. If no files can be selected, a 400 response includes the results and no task is created. Only the first 100 results are retained; `input_results_omitted` reports the rest, without limiting actual link processing. Metadata contains neither URLs nor raw Telegram errors. Selection is not download completion or proof of a complete media group; the existing original-message fallback on album lookup failure is preserved. Webhooks do not gain these fields.

Known album lookup errors or empty results add `album_fallback: true`, meaning only the original message was considered. This flag is independent of selection status: a newly selected original remains ready, a previously selected original remains duplicate, and unsupported media remains failed. It does not estimate missing members. The field is omitted without known fallback; `?single` and successful queries returning just one member are not flagged based on count. Request cancellation does not trigger fallback or admit partially prepared files.

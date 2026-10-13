# Review records

The local web usability follow-up is [2026-10-13-web-management-usability.md](2026-10-13-web-management-usability.md): one-source/one-target dropdowns, chat search, durable account-scoped chat/Topic snapshots with explicit sync, radio choices, basic keyword filters and local validation. These changes are not yet published or deployed.

The current candidate is [v2.0.0-rc.5](https://github.com/beihehele/SaveAny-Bot/releases/tag/v2.0.0-rc.5), code commit `634b653`, published from `dev`. [The rc.5 release and acceptance report](2026-10-12-rc5-release-acceptance.md) records successful candidate CI, published binary/image identity, the official-image fallback after mirror timeouts, WSL deployment and real Telegram checks. [The consolidated backlog report](2026-10-12-backlog-remediation.md) preserves implementation reviews and remaining acceptance limits. Earlier candidate preparation, rc.4 history/media and deployment reports are historical evidence. WSL is the acceptance environment; NAS migration remains deferred.

Dated reports describe the code and checks at that point in time. Reports preceding the Telegram-only cleanup include features no longer supported and are historical evidence, not current setup instructions.

History-budget commit `4df6156`, fallback metadata commit `bf33a05`, route admission commit `d3ed987` and consolidated implementation commit `634b653` have been pushed. Descriptions of local/uncommitted snapshots in older dated reports refer to those historical states; rc.5 supersedes the deployed rc.4 candidate.

The initial cleanup scope and verification are recorded in `2026-10-10-telegram-only-cleanup.md`; later findings and fixes can supersede its conclusions. Release readiness must be evaluated against the exact candidate revision. Active usage/configuration documentation lives under `docs/content`.

The final section of the backlog report superseded an earlier no-blocker conclusion after preference consistency, copy pagination and Bot default-directory problems were reproduced and fixed. Windows full/race/vet/build and WSL full/vet/SQLite/build/offline-runtime checks passed before rc.5 packaging. Candidate CI and scoped live acceptance now have evidence in the rc.5 report. Controlled real timeout recovery, interleaved arrival timing, bounded-group completeness limits and the final NAS deployment are still separate from the completed checks.

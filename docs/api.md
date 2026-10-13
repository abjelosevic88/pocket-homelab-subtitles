# API

All endpoints are under `/api/v1` and need a session cookie or `Authorization: Bearer <token>` (Settings → API tokens). JSON in, JSON out.

| Method & path | What |
|---|---|
| `GET /overview` | Counters, running jobs with stage, next 25 queued, 8 recent runs, 14 days of activity, sweep schedule. |
| `GET /widget` | Compact status for dashboards: `status`, `now`, `queued`, `missing`, `attention`, `processed`, `adjusted`, `today`, `items[{name,detail}]`. |
| `POST /hooks/bazarr` | Bazarr's post-processing (form or JSON: `video`, `subtitle`, `language`, `provider`, `subtitle_id`, `series_id`, `episode_id`). 202 with the job. |
| `POST /sync` | Queue one file: `{"path": "...", "force": false}`. |
| `GET /queue` · `POST /queue/{id}/cancel` · `POST /queue/{id}/bump` · `POST /queue/clear` | The queue. |
| `GET /runs?q=&outcome=&source=&lang=&limit=&offset=` · `GET /runs/{id}` | History (`outcome`: adjusted, in_sync, attention, rejected). A run's `details` is the engine's full JSON. |
| `POST /runs/{id}/restore` | Put a rejected file back (and pin it). |
| `GET /library/titles?q=&status=` · `GET /library/files?title=&season=&q=&status=&lang=` · `GET /library/langs` · `POST /library/scan` | The library (`status`: attention, pinned, nudged, unchecked, synced, in_sync, missing). |
| `GET /files/{id}` · `GET /files/by-path?path=` · `GET /files/{id}/text` | One file with runs, events, `has_original`, `can_undo`, queued job. |
| `POST /files/{id}/sync` `{unpin}` · `/nudge` `{ms}` · `/undo` · `/pin` `{note}` · `/unpin` · `/restore-original` · `/verify` | Actions. |
| `GET /nudges` | Recent nudges. |
| `GET /tasks` · `GET /tasks/{id}` · `POST /tasks/{id}/cancel` | Sweeps and season tools, with logs and their jobs. |
| `GET /sweep/plan` · `POST /sweep` | What a sweep would check; start one now. |
| `POST /seasons` `{title, season, mode: verify|resync|reset, fresh}` | Season tools (reset: admins). |
| `GET /issues/missing` · `POST /issues/missing/search` | Bazarr's wanted list; search one language now. |
| `GET/PUT /settings` · `GET /settings/system` · `POST /settings/test/{bazarr|jellyfin|notify}` · `GET /doctor` | Settings (API keys are write-only), system info, connection tests, engine toolchain. |
| `GET /backup` | Database download (admins). |
| users, tokens, auth | As in the other Pocket apps. |

Outside `/api/v1`: `/healthz`, `/readyz`, `/metrics`.

## Homepage widget

```yaml
- Subtitles:
    href: https://subs.example.com
    icon: mdi-subtitles-outline
    widgets:
      - type: customapi
        url: http://pocket-subtitles:8080/api/v1/widget
        headers: { Authorization: "Bearer {{HOMEPAGE_VAR_POCKET_SUBTITLES_TOKEN}}" }
        mappings:
          - { field: now, label: Now syncing }
          - { field: queued, label: In queue }
          - { field: attention, label: Attention }
          - { field: missing, label: Missing }
      - type: customapi
        url: http://pocket-subtitles:8080/api/v1/widget
        headers: { Authorization: "Bearer {{HOMEPAGE_VAR_POCKET_SUBTITLES_TOKEN}}" }
        display: dynamic-list
        mappings: { items: items, name: name, label: detail, limit: 8 }
```

# Configuration

Two layers:

- **Settings in the UI** (stored in the database): sync behaviour, quality gates, the nightly sweep, integrations, notifications, season tools. Admins edit them under Settings.
- **Environment variables**: where things live, how people sign in, how hard the engine may work. Integration variables *seed* the matching settings once (empty fields only, on the first start of this version); what is saved in the UI wins afterwards, and a Disconnect stays cleared across restarts.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `HOST` / `PORT` | `0.0.0.0` / `8080` | Listen address. |
| `BASE_URL` | – | Public URL (informational). |
| `TZ` | `UTC` | Time zone. The sweep starts at Settings → *Start at* on this clock. |
| `DATA_DIR` | `/data` (image) | Everything the app writes. |
| `DB_PATH` | `$DATA_DIR/pocket-subtitles.db` | SQLite database (WAL mode). |
| `CACHE_DIR` | `$DATA_DIR/cache` | Per-video measurements: speech references (`.npz`), act boundaries, extracted embedded tracks and their verdicts. Keyed by file name + size. |
| `BACKUP_DIR` | `$DATA_DIR/backups` | First originals (mirroring the library: `shows/Show/…`), `manual/nudge/` undo copies, `manual/restore/`, season reset backups. |
| `REJECT_DIR` | `$DATA_DIR/rejected` | Copies of rejected downloads (`name.srt.<unix time>`). |
| `MEDIA_DIRS` | `/media` | Library folders, comma separated. The first folder below each is taken as the show or movie name; files directly in a folder are named from the file name. |
| `SCAN_INTERVAL` | `15m` | How often the library is walked for new, renamed or deleted files. |
| `WORKERS` | `1` | Engine processes in parallel. Each one decodes a whole video's audio; 1 is plenty for a home library. |
| `NICE` | `19` | Engine CPU priority. |
| `JOB_TIMEOUT` | `1h` | Per file. |
| `ENGINE_PYTHON` / `ENGINE_SCRIPT` | `python3` / `/app/engine/pocket_sync.py` | Only for running outside the image. |
| `ALASS_BIN` | `alass-cli` | Path to alass. |
| `FFSUBSYNC_LIBS` | – | Extra Python import path for ffsubsync (e.g. Bazarr's vendored libs when running the engine inside Bazarr's container). |
| `AUTH_MODE` | `local` | `local` (built-in accounts), `proxy` (trust `AUTH_PROXY_HEADER`, users auto-created), `none` (one implicit admin; trusted LAN only). |
| `AUTH_PROXY_HEADER` / `AUTH_PROXY_EMAIL_HEADER` | `Remote-User` / `Remote-Email` | For `proxy` mode. |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | First admin, created when there are no users yet. |
| `SESSION_TTL` | `720h` | Login lifetime. |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | – | Sign in with an OpenID Connect provider (PocketID, Authelia, Authentik, Keycloak…). Needs `BASE_URL`; the callback is `BASE_URL/api/v1/auth/oidc/callback`. On a box with Pocket Core none of these are needed — the app receives its client when it registers. |
| `OIDC_NAME` | `single sign-on` | Button label: “Sign in with …” (“Pocket Core” when the client came from Core). |
| `OIDC_AUTO_CREATE` | `true` | Give an unknown (verified) email a member account instead of refusing it. Always on for the Core-issued client. |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS. |
| `TRUST_PROXY` | `true` | Use `X-Forwarded-For` for client addresses. |
| `METRICS_ENABLED` | `true` | Prometheus text at `/metrics`. |
| `LEGACY_API` | `off` | `open` / `token`: serve `GET /` (JSON for non-browser clients), `GET /nudge/files`, `POST /nudge/{apply,undo,unpin}` like the old sync-status-server. |
| `BAZARR_URL`, `BAZARR_API_KEY`, `BAZARR_PATH_MAP` | – | Seed Settings → Integrations → Bazarr (Direct), once. `BAZARR_API_KEY_FILE` reads a file. Not needed on a box with Pocket Core (see below). |
| `JELLYFIN_URL`, `JELLYFIN_API_KEY`, `JELLYFIN_PATH_MAP` | – | Seed Settings → Integrations → Jellyfin (Direct), once. Not needed on a box with Pocket Core. |
| `CORE_URL` / `CORE_REGISTRATION_KEY` | – | [Pocket Core](https://git.bjelke.org/abjelosevic88/pocket-homelab-core): register on start (the key is needed once; the minted app token lives in `$DATA_DIR/core.token`), publish `subtitles.synced` / `subtitles.failed` / `subtitles.nudged`, and accept the tokens Core hands to paired phones |
| `CORE_APP_URL` | `BASE_URL` | How Core reaches this app for health checks (e.g. `http://host.docker.internal:8150`) |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug` logs every request; `json` for log shippers. |
| `DEMO_DATA` | `false` | Sample library and history in an empty database; nothing is synced. |

## Settings (UI)

**Sync → Timing**

- *Reading lead* (ms): audio alignment puts a cue on the first syllable; a lead moves every cue that much after the voice. 0 = pure audio timing. The lead is applied last and idempotently: a file already carrying it measures as in sync.
- *Centre channel*: on 5.1 audio, build a second speech reference from the dialogue channel and use it when it explains the subtitle better than the full mix (decided per video, cached).
- *Polish*: re-measure each cue with its neighbours; move a block only when gain, margin, landing on speech and neighbour agreement all hold.
- *Re-measure embedded-track results*: off by default.

**Sync → Quality gates**

- *Reject wrong content*: cues must cover the detected speech (floor 45%, or 60% of what the video's own trusted track achieves).
- *Machine translation* languages: fresh downloads only.
- *On rejection*: `blacklist` (copy kept, file removed, Bazarr blacklists the upload and searches again), `quarantine` (copy kept, file removed), `keep` (only flagged).
- *Cyrillic → Latin* languages.

**Sync → Nightly sweep**: on/off, start time, time budget, *re-check verdicts older than* (after an engine change), managed languages, auto-unpin on a new download, verification tolerance.

**Integrations**: Bazarr and Jellyfin, each with a **Pocket Core | Off** switch (saved at once; with no Pocket Core, or while Direct is in use, **Pocket Core | Direct | Off**, else Direct sits behind *Advanced: connect directly with a URL and key*) and a path mapping; notifications (Apprise API URL, ntfy topic URL, or a webhook receiving `{title, body, level, app}`), season tools (languages, manual-search languages, preferred provider).

## Path mappings

- **Bazarr → app**: `bazarr-prefix=app-prefix`, e.g. `/tv=/media/shows,/movies=/media/movies`. Used for paths in the hook and the season tools. Simplest is to mount the library at the same path in both containers and leave it empty.
- **App → Jellyfin**: `app-prefix=jellyfin-prefix`, e.g. `/media=/data/media`, when Jellyfin sees the library elsewhere.

## Bazarr and Jellyfin through Pocket Core

Both are box-wide: the app syncs subtitles for the whole library, not for one person. Each has three modes under Settings → Integrations:

| Mode | How it is reached |
|---|---|
| **Pocket Core** | Pocket Core's service connection (`services: bazarr, jellyfin` in the manifest): Core hands out the service's address and key (`POST /api/v1/services/credential`); the Core SDK reuses it for about a minute, so switching it off in Core applies within a minute (a refusal at once) and a Core restart does not stop syncs. **Test** always asks Core afresh. The app keeps no key itself. The app acts as **the admin** (`ADMIN_EMAIL` when that account exists, else the first admin), so Core gives that admin's own key there or, failing that, the box's key (Core's rule for admins). Links (Issues → *Open Bazarr*) use Core's public URL. |
| **Direct** | The URL and API key saved here (how it always worked). **Disconnect** removes both. |
| **Off** | No calls at all: no missing counts, no blacklisting, no season reset, no Jellyfin refresh. |

With no mode stored: Direct when a key is saved (an existing setup keeps working), else Pocket Core when `CORE_URL` is set, else Direct. The card shows *Connected through Pocket Core (the admin's own key / the box's key), as the admin …*, a direct connection, or *Not connected:* with Core's reason (switched off for the box or by the admin, no key there, no such service in Core…). The status asks Core only for the link state (`GET /api/v1/links/status`), never the key. Path mappings apply in every mode.

The modes in effect are reported to Core for the admin the app acts as (`PUT /api/v1/links/modes`, targets `service:bazarr` and `service:jellyfin`) after registering, every 30 minutes and right after a switch or a Disconnect, so Core's Links page shows Direct connections too. Report only; failures are logged.

`GET /api/v1/settings` carries `bazarr_mode` / `jellyfin_mode` (in effect; a PUT stores them), `*_via` (`core` | `direct` | empty), `*_source` (`personal` | `box`, from the last use), `*_detail` and `services_as`. `POST /api/v1/settings/test/{bazarr,jellyfin}` follows the mode.

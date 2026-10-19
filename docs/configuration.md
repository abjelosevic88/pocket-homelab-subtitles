# Configuration

Two layers:

- **Settings in the UI** (stored in the database): sync behaviour, quality gates, the nightly sweep, integrations, notifications, season tools. Admins edit them under Settings.
- **Environment variables**: where things live, how people sign in, how hard the engine may work. Integration variables only *seed* the matching settings on first start; what is saved in the UI wins afterwards.

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
| `BAZARR_URL`, `BAZARR_API_KEY`, `BAZARR_PATH_MAP` | – | Seed Settings → Integrations → Bazarr. `BAZARR_API_KEY_FILE` reads a file. |
| `JELLYFIN_URL`, `JELLYFIN_API_KEY`, `JELLYFIN_PATH_MAP` | – | Seed Settings → Integrations → Jellyfin. |
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

**Integrations**: Bazarr (URL, key, path mapping), Jellyfin (URL, key, path mapping), notifications (Apprise API URL, ntfy topic URL, or a webhook receiving `{title, body, level, app}`), season tools (languages, manual-search languages, preferred provider).

## Path mappings

- **Bazarr → app**: `bazarr-prefix=app-prefix`, e.g. `/tv=/media/shows,/movies=/media/movies`. Used for paths in the hook and the season tools. Simplest is to mount the library at the same path in both containers and leave it empty.
- **App → Jellyfin**: `app-prefix=jellyfin-prefix`, e.g. `/media=/data/media`, when Jellyfin sees the library elsewhere.

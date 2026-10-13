# Migrating from the sync_hook.py scripts

Pocket Subtitles grew out of a set of scripts around Bazarr: `sync_hook.py` (Bazarr's post-processing, inside its container), `sync-sweep.py` (nightly, via `docker exec`), `sync-status-server.py` + `nudge.py` (status page and manual shifts on :8150). If you ran those, everything carries over.

1. **Stop the old pieces**: the status server and the sweep timer (`systemctl --user disable --now subsync-status.service subsync-sweep.timer`), and Bazarr (its config is rewritten in step 5).
2. **Copy the state** into the new data volume: `sync-cache/` → `data/cache`, `sync-backups/` → `data/backups`, `sync-rejected/` → `data/rejected`. The cache keys and file formats are the same; the engine pins the same ffsubsync version Bazarr vendors.
3. **Import the history**:

   ```bash
   docker compose run --rm -v /path/to/bazarr/config:/legacy:ro pocket-subtitles import-legacy \
     -dir /legacy -map /host/media=/media \
     -backups /path/to/bazarr/config/sync-backups=/data/backups
   ```

   It scans the library, then imports `sync-ledger.jsonl` (per-file verdicts *with their file signatures*, so the first sweep skips everything unchanged), `sync-history.jsonl` (every run), `sync-pinned.txt` (pins, with the preceding comment as the note) and `nudge-log.jsonl` (nudges with their undo backups, net shift per file). `-reverify-before YYYY-MM-DD` carries over a re-verification cut-off.
4. **Settings**: the old environment flags become settings — `SYNC_LEAD_OUT=0.3` → reading lead 300 ms, `SYNC_CENTRE_REF=1` → centre channel, `SYNC_POLISH_APPLY=1` → polish, `SYNC_REF_RESIDUAL` → re-measure; Serbian latinisation → *Cyrillic → Latin: sr*; the MT gate's `sr,hr`.
5. **Bazarr**: replace `postprocessing_cmd` with the hook command (Settings → Integrations), with Bazarr stopped — it rewrites `config.yaml` on save.
6. **Old clients**: `LEGACY_API=open` keeps `GET /` (JSON for non-browsers), `GET /nudge/files` and `POST /nudge/{apply,undo,unpin}` answering exactly as before; publish the app on the old port and dashboards, shortcuts and scripts keep working. `/history` and `/nudge` open the new pages.

Rollback: re-enable the two units, put the old `postprocessing_cmd` back, stop the container. Nothing in the old directories is modified by the import.

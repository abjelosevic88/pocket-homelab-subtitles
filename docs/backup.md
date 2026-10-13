# Backup

| What | Where | Back up? |
|---|---|---|
| Database | `DATA_DIR/pocket-subtitles.db` (+ `-wal`, `-shm`) | **Yes**, through a snapshot: Settings → Backup & data, `pocket-subtitles backup`, or `GET /api/v1/backup` (`VACUUM INTO`). Do not copy the live WAL files raw. |
| Originals & undo copies | `DATA_DIR/backups/` | **Yes**. The only copy of what providers delivered before the app changed it. Small (text). |
| Rejected downloads | `DATA_DIR/rejected/` | Optional. |
| Measurement cache | `DATA_DIR/cache/` | Optional. Rebuildable, but each entry costs a full audio decode of its video (minutes on a NAS). |

With restic/Borg/Backrest: run `docker exec pocket-subtitles pocket-subtitles backup -o /data/backups/db/latest.db` before the snapshot, back up `DATA_DIR`, and exclude `pocket-subtitles.db*`.

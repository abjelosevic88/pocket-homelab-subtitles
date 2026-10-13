# Bazarr

## The hook

Bazarr runs a *custom post-processing* command after every subtitle it downloads. Pocket Subtitles' command is a `curl` that hands the file to the queue and returns immediately — Bazarr is never kept waiting for a sync:

```
curl -fsS -m 20 -H "Authorization: Bearer pst_…" --data-urlencode video={{episode}} --data-urlencode subtitle={{subtitles}} --data-urlencode language={{subtitles_language_code2}} --data-urlencode provider={{provider}} --data-urlencode subtitle_id={{subtitle_id}} --data-urlencode series_id={{series_id}} --data-urlencode episode_id={{episode_id}} http://pocket-subtitles:8080/api/v1/hooks/bazarr
```

Settings → Integrations → *Connect Bazarr's downloads* builds it for you, token included. Notes:

- Bazarr wraps each `{{…}}` value in double quotes and splits the command like a shell (no shell runs it), so `video={{episode}}` becomes one argument even with spaces in the path. Keep the form `name={{var}}` as above.
- The provider, subtitle id and series/episode ids let the app **blacklist** a rejected download so Bazarr fetches a different one. Without them a rejected file is simply removed (or kept, per Settings).
- Turn Bazarr's own *Automatic subtitles synchronization* (Subtitles → Subtitle Synchronization) **off**; two syncs on one file fight each other.
- The `curl` in the Bazarr image (linuxserver.io) is enough; nothing else needs installing.

Bazarr must reach the app: on a shared Docker network use the service name (`http://pocket-subtitles:8080`); otherwise the host's address and published port (`http://host.docker.internal:8150` with `extra_hosts: ["host.docker.internal:host-gateway"]`).

## Paths

The hook sends Bazarr's paths. If both containers mount the library at the same path, nothing to do. Otherwise set Settings → Integrations → Bazarr → *Path mapping* (`bazarr-prefix=app-prefix`). The hook answers 400 with a hint when a path is outside `MEDIA_DIRS`.

## API key

Settings → Integrations → Bazarr URL + API key enables:

- the *Missing downloads* tile and Issues → Missing subtitles (with *search now* per language),
- blacklisting rejected downloads,
- Season → *Reset…*.

## Season reset

Library → a show → a season → *Reset…* (admins):

1. unpins the season and backs every subtitle up to `backups/manual/<show>-sNN-<time>/`,
2. moves the season's cached measurements aside (fresh audio references),
3. deletes the subtitles through Bazarr (its database stays consistent),
4. downloads each episode × language again, one at a time: the automatic search, or — for the languages listed under *manual provider search* — Bazarr's manual search, ranked preferred provider first, Cyrillic packs last when the language is kept in Latin, non-HI before HI, then score. A `.xx.hi.srt` that arrives without a plain twin is renamed to `.xx.srt` after its sync,
5. every arrival is synced through the hook; anything not synced within the run is queued,
6. anything no provider has, by either search, is put back from the backup (and logged),
7. removes HI duplicates of a plain file through Bazarr,
8. verifies every file independently.

The log of each step is on the task page.

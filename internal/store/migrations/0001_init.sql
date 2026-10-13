CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

-- One row per external subtitle file in the library (the ledger). The *_size /
-- *_mtime pair is the file signature at the last verdict: an unchanged pair
-- means the verdict still holds and the nightly sweep skips the file.
CREATE TABLE subtitles (
  id              INTEGER PRIMARY KEY,
  path            TEXT NOT NULL UNIQUE,
  video_path      TEXT NOT NULL DEFAULT '',
  rel             TEXT NOT NULL DEFAULT '',       -- library folder + path, e.g. shows/Dark/S01/x.sr.srt
  library         TEXT NOT NULL DEFAULT '',       -- the library folder name (shows, movies, …)
  title           TEXT NOT NULL DEFAULT '',       -- show or movie, e.g. "Dark (2017)"
  kind            TEXT NOT NULL DEFAULT 'other',  -- episode | movie | other
  season          INTEGER,
  episode         INTEGER,
  label           TEXT NOT NULL DEFAULT '',       -- S01E02, or the movie title
  lang            TEXT NOT NULL DEFAULT '',       -- sr, en, pt-br …
  flags           TEXT NOT NULL DEFAULT '',       -- hi, forced, sdh
  sub_size        INTEGER,
  sub_mtime       REAL,
  vid_size        INTEGER,
  vid_mtime       REAL,
  last_decision   TEXT NOT NULL DEFAULT '',
  last_reason     TEXT NOT NULL DEFAULT '',
  last_method     TEXT NOT NULL DEFAULT '',
  last_shift      REAL,
  last_run_id     INTEGER,
  last_checked_at TEXT,
  pinned          INTEGER NOT NULL DEFAULT 0,
  pin_note        TEXT NOT NULL DEFAULT '',
  pinned_at       TEXT,
  nudge_ms        INTEGER NOT NULL DEFAULT 0,
  verify          TEXT NOT NULL DEFAULT '',       -- JSON of the last independent check
  verified_at     TEXT,
  missing         INTEGER NOT NULL DEFAULT 0,     -- not found by the last library scan
  seen_at         TEXT,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX subtitles_title ON subtitles(title);
CREATE INDEX subtitles_decision ON subtitles(last_decision);

-- Every completed engine run: the history.
CREATE TABLE runs (
  id          INTEGER PRIMARY KEY,
  subtitle_id INTEGER REFERENCES subtitles(id) ON DELETE SET NULL,
  path        TEXT NOT NULL DEFAULT '',
  name        TEXT NOT NULL,                      -- file name at the time
  title       TEXT NOT NULL DEFAULT '',
  label       TEXT NOT NULL DEFAULT '',
  lang        TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT '',           -- bazarr | sweep | manual | season | api | import
  decision    TEXT NOT NULL,
  method      TEXT NOT NULL DEFAULT '',
  max_shift   REAL,
  content_fit REAL,
  ref_fit     REAL,
  reason      TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  details     TEXT NOT NULL DEFAULT '{}',
  job_id      INTEGER,
  created_at  TEXT NOT NULL
);
CREATE INDEX runs_created ON runs(created_at);
CREATE INDEX runs_subtitle ON runs(subtitle_id);
CREATE INDEX runs_decision ON runs(decision);

-- The queue. Finished jobs are kept for a while for the task views.
CREATE TABLE jobs (
  id          INTEGER PRIMARY KEY,
  path        TEXT NOT NULL,
  video_path  TEXT NOT NULL DEFAULT '',
  title       TEXT NOT NULL DEFAULT '',
  label       TEXT NOT NULL DEFAULT '',
  lang        TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL,
  priority    INTEGER NOT NULL DEFAULT 0,
  extras      TEXT NOT NULL DEFAULT '{}',         -- Bazarr ids of a fresh download
  force       INTEGER NOT NULL DEFAULT 0,
  state       TEXT NOT NULL DEFAULT 'queued',     -- queued | running | done | failed | cancelled
  stage       TEXT NOT NULL DEFAULT '',
  run_id      INTEGER,
  task_id     INTEGER,
  error       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  started_at  TEXT,
  finished_at TEXT
);
CREATE INDEX jobs_state ON jobs(state, priority DESC, id);
CREATE INDEX jobs_task ON jobs(task_id);

-- Long-running work with a log: sweeps and season tools.
CREATE TABLE tasks (
  id          INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL,                      -- sweep | season_resync | season_verify | season_reset | verify
  title       TEXT NOT NULL,
  state       TEXT NOT NULL DEFAULT 'running',    -- running | done | failed | cancelled
  params      TEXT NOT NULL DEFAULT '{}',
  summary     TEXT NOT NULL DEFAULT '{}',
  log         TEXT NOT NULL DEFAULT '',
  total       INTEGER NOT NULL DEFAULT 0,
  done        INTEGER NOT NULL DEFAULT 0,
  user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  finished_at TEXT
);

-- Manual ±ms shifts with their undo trail.
CREATE TABLE nudges (
  id         INTEGER PRIMARY KEY,
  path       TEXT NOT NULL,
  ms         INTEGER NOT NULL,
  backup     TEXT NOT NULL DEFAULT '',
  cues       INTEGER NOT NULL DEFAULT 0,
  undone     INTEGER NOT NULL DEFAULT 0,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX nudges_path ON nudges(path);

-- What people did to a file (pin, unpin, restore, …) and other non-sync events.
CREATE TABLE events (
  id         INTEGER PRIMARY KEY,
  path       TEXT NOT NULL,
  kind       TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX events_path ON events(path);

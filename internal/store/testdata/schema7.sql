-- tracks.db as schema 7 left it, for the schema 8 migration test.
CREATE TABLE IF NOT EXISTS tracks (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  mode        TEXT NOT NULL DEFAULT 'focus',
  tier        TEXT NOT NULL DEFAULT '',
  tier_why    TEXT NOT NULL DEFAULT '',
  dir         TEXT NOT NULL DEFAULT '',
  rex_session TEXT NOT NULL DEFAULT '',
  claude_session TEXT NOT NULL DEFAULT '',
  claude_cwd  TEXT NOT NULL DEFAULT '',
  check_text  TEXT NOT NULL DEFAULT '',
  waiting_on  TEXT NOT NULL DEFAULT '',
  links       TEXT NOT NULL DEFAULT '',
  goal        TEXT NOT NULL DEFAULT '',
  where_text  TEXT NOT NULL DEFAULT '',
  standing    TEXT NOT NULL DEFAULT '',
  later       TEXT NOT NULL DEFAULT '',
  extra       TEXT NOT NULL DEFAULT '{}',
  answered_at TEXT,
  updated_at  TEXT NOT NULL,
  changed_at  TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  closed_at   TEXT,
  version     INTEGER NOT NULL DEFAULT 1,
  render_hash TEXT NOT NULL DEFAULT '',
  rank        INTEGER,
  today       TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS names (
  name       TEXT PRIMARY KEY COLLATE NOCASE,
  track_id   INTEGER NOT NULL REFERENCES tracks(id),
  retired_at TEXT
);
CREATE TABLE IF NOT EXISTS checks (
  id       INTEGER PRIMARY KEY,
  track_id INTEGER NOT NULL REFERENCES tracks(id),
  text     TEXT NOT NULL,
  answer   INTEGER NOT NULL,
  asked_at TEXT,
  ran_at   TEXT,
  UNIQUE (track_id, text)
);
CREATE TABLE IF NOT EXISTS asks (
  id          INTEGER PRIMARY KEY,
  track_id    INTEGER NOT NULL REFERENCES tracks(id),
  kind        TEXT NOT NULL,
  text        TEXT NOT NULL,
  position    INTEGER NOT NULL,
  check_id    INTEGER REFERENCES checks(id),
  created_at  TEXT NOT NULL,
  seen_at     TEXT,
  answered_at TEXT,
  closed_at   TEXT,
  closed_by   TEXT
);
CREATE INDEX IF NOT EXISTS asks_open ON asks(track_id, closed_at);
CREATE TABLE IF NOT EXISTS blockers (
  track_id   INTEGER NOT NULL REFERENCES tracks(id),
  blocker_id INTEGER NOT NULL REFERENCES tracks(id),
  position   INTEGER NOT NULL,
  PRIMARY KEY (track_id, blocker_id)
);
CREATE TABLE IF NOT EXISTS events (
  id          INTEGER PRIMARY KEY,
  at          TEXT NOT NULL,
  event       TEXT NOT NULL,
  track_id    INTEGER REFERENCES tracks(id),
  track_name  TEXT NOT NULL DEFAULT '',
  rex_session TEXT,
  rex_block   TEXT,
  cwd         TEXT,
  len         INTEGER,
  kind        TEXT
);
CREATE INDEX IF NOT EXISTS events_track_at ON events(track_id, at);
CREATE TABLE IF NOT EXISTS desk (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  rex_session TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS desk_asks (
  id             INTEGER PRIMARY KEY,
  track          TEXT NOT NULL,
  question       TEXT NOT NULL,
  recommendation TEXT NOT NULL DEFAULT '',
  evidence       TEXT NOT NULL DEFAULT '[]',
  created_at     TEXT NOT NULL,
  closed_at      TEXT,
  answer_kind    TEXT NOT NULL DEFAULT '',
  answer         TEXT NOT NULL DEFAULT '',
  answered_at    TEXT,
  picked_up_at   TEXT,
  raised_by      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS desk_log (
  id    INTEGER PRIMARY KEY,
  at    TEXT NOT NULL,
  track TEXT NOT NULL,
  kind  TEXT NOT NULL,
  asked TEXT NOT NULL DEFAULT '',
  did   TEXT NOT NULL,
  undone_at TEXT
);
CREATE INDEX IF NOT EXISTS desk_log_at ON desk_log(at);
PRAGMA user_version = 7;
INSERT INTO tracks (id, name, claude_session, claude_cwd, updated_at, changed_at, created_at)
  VALUES (1, 'alpha', '11111111-1111-1111-1111-111111111111', '/work/alpha', '2026-11-20T10:00:00.000000000Z', '2026-11-20T10:00:00.000000000Z', '2026-11-20T10:00:00.000000000Z');
INSERT INTO tracks (id, name, updated_at, changed_at, created_at)
  VALUES (2, 'beta', '2026-11-20T10:00:00.000000000Z', '2026-11-20T10:00:00.000000000Z', '2026-11-20T10:00:00.000000000Z');
INSERT INTO tracks (id, name, claude_session, claude_cwd, updated_at, changed_at, created_at, closed_at)
  VALUES (3, 'gamma', '33333333-3333-3333-3333-333333333333', '/work/gamma', '2026-11-20T10:00:00.000000000Z', '2026-11-20T10:00:00.000000000Z', '2026-11-20T10:00:00.000000000Z', '2026-11-21T10:00:00.000000000Z');
INSERT INTO names (name, track_id) VALUES ('alpha', 1), ('beta', 2), ('gamma', 3);
INSERT INTO desk_asks (track, question, created_at) VALUES ('alpha', 'ship it?', '2026-11-20T10:00:00.000000000Z');

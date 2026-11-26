package store

const schemaVersion = ordersSchema

const schema = `
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
CREATE TABLE IF NOT EXISTS sessions (
  id          TEXT PRIMARY KEY,
  track_id    INTEGER REFERENCES tracks(id),
  parent_id   TEXT,
  owner       TEXT NOT NULL DEFAULT 'emil' CHECK (owner IN ('emil', 'desk')),
  host        TEXT NOT NULL DEFAULT 'rex' CHECK (host IN ('rex', 'background', 'stopped')),
  name        TEXT NOT NULL DEFAULT '',
  cwd         TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'working', 'done', 'blocked', 'error')),
  waiting_for TEXT NOT NULL DEFAULT '',
  activity    TEXT NOT NULL DEFAULT '',
  last_answer TEXT NOT NULL DEFAULT '',
  seen_at     TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  ended_at    TEXT
);
CREATE INDEX IF NOT EXISTS sessions_track ON sessions(track_id);
CREATE TABLE IF NOT EXISTS session_subagents (
  session_id TEXT NOT NULL REFERENCES sessions(id),
  agent_id   TEXT NOT NULL,
  name       TEXT NOT NULL,
  status     TEXT NOT NULL CHECK (status IN ('queued', 'working', 'done')),
  updated_at TEXT NOT NULL,
  PRIMARY KEY (session_id, agent_id)
);
CREATE TABLE IF NOT EXISTS questions (
  id            INTEGER PRIMARY KEY,
  track_id      INTEGER REFERENCES tracks(id),
  session_id    TEXT REFERENCES sessions(id),
  from_kind     TEXT NOT NULL CHECK (from_kind IN ('agent', 'desk', 'check')),
  question      TEXT NOT NULL,
  options       TEXT NOT NULL DEFAULT '[]' CHECK (json_array_length(options) <= 9),
  recommended   INTEGER,
  recommendation TEXT NOT NULL DEFAULT '',
  ask_id        INTEGER,
  evidence      TEXT NOT NULL DEFAULT '[]',
  links         TEXT NOT NULL DEFAULT '',
  attachments   TEXT NOT NULL DEFAULT '[]',
  created_at    TEXT NOT NULL,
  answer_option INTEGER,
  answer_text   TEXT NOT NULL DEFAULT '',
  answered_by   TEXT NOT NULL DEFAULT '',
  answered_at   TEXT,
  delivered_at  TEXT,
  closed_at     TEXT
);
CREATE TABLE IF NOT EXISTS messages (
  id           INTEGER PRIMARY KEY,
  session_id   TEXT NOT NULL REFERENCES sessions(id),
  from_kind    TEXT NOT NULL CHECK (from_kind IN ('emil', 'desk', 'session', 'system')),
  from_session TEXT,
  kind         TEXT NOT NULL CHECK (kind IN ('note', 'answer', 'report')),
  text         TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  held         INTEGER NOT NULL DEFAULT 0,
  delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS messages_session ON messages(session_id, delivered_at);
CREATE TABLE IF NOT EXISTS conversation (
  id           INTEGER PRIMARY KEY,
  at           TEXT NOT NULL,
  role         TEXT NOT NULL CHECK (role IN ('emil', 'desk', 'report')),
  text         TEXT NOT NULL,
  steps        INTEGER NOT NULL DEFAULT 0,
  session_id   TEXT NOT NULL DEFAULT '',
  outcome      TEXT NOT NULL DEFAULT '',
  delivered_at TEXT
);
CREATE TABLE IF NOT EXISTS desk_orders (
  id         INTEGER PRIMARY KEY,
  text       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  dropped_at TEXT
);
CREATE TABLE IF NOT EXISTS desk_queue (
  id         INTEGER PRIMARY KEY,
  track_id   INTEGER NOT NULL REFERENCES tracks(id),
  parent_id  TEXT NOT NULL,
  model      TEXT NOT NULL DEFAULT '',
  prompt     TEXT NOT NULL,
  queued_at  TEXT NOT NULL,
  started_at TEXT,
  error      TEXT NOT NULL DEFAULT ''
);
`

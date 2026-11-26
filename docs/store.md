# Track state in a store (EMI-307)

Status: proposal, 2026-11-20. Tier 3: it migrates live state that unattended code writes (the hook, `tracks check` under launchd, resume on Rex server start).

## Decision

Move track state to SQLite (`$TRACKS_HOME/tracks.db`, pure-Go driver, WAL) as the only source of truth. Keep `cards/NAME.md` and `cards/archive/*.md` as files rendered from it, in today's format, after every write. Cut over once, with a backup and a tested export back to files. No dual-write.

The case is not speed or size: the whole state is under 1 MB (7 live cards, 69 archived, 1,860 events, 2 check records). The case is that the code already is a hand-built database, and it keeps losing to edge cases that a database gets right for free. Asks and renamed tracks have no identity, so every fix has been one more text match, one more log replay or one more file-system trick.

## 1. Problem

What the files force on the code today, with the failures it has caused:

| # | Mechanism | Failures it caused |
|---|---|---|
| P1 | **Asks have no identity.** An ask is a numbered line under `## Needs me`. A check ask is recognised by regex (`checkAskShape`, `checkAskQuote`), and its "has Emil seen it" state is spread over `checks.json` (`asked`, keyed `"NAME: CHECK"`), `open`/`answer`/`prompt` events in `attention.jsonl` (`seenSince`), and on the card itself. An agent's ask carries no creation time at all: `ls --json` gives every ask the card file's mtime as `since`, so Meridian keeps its own first-seen record. | EMI-277: an agent's rewrite from an old copy dropped check asks, so `tracks check` gained `askLost` to put them back. EMI-308: changing `check:` without `asks:` orphaned a seen ask that then survived every write. EMI-318 (open): a check that flips yes to no leaves the "yes" ask with a "no" record, and it survives every `asks:` write. `applyCheckAsk` matches the ask by its exact text and number, so any rewrite in between turns into "it changed on the card". |
| P2 | **Answered state lives in three places**: `answered:` (hook), `answered_asks: <time> 1,2` (pane, valid only within 2 minutes of `answered:`), and `updated:`, which they are compared against. Stop and SessionStart clear both. | The pairing rules in `need.go` (`answeredFor`, `answeredPair`) and the 6-second `answeredGrace` in the pane exist to reconcile them. |
| P3 | **A rename moves a file and logs an event.** Track identity is the file name. `tracks rename` hard-links `NEW.md`, takes `OLD.md` away with a rename, re-reads the taken file and logs a `rename` event. Every reader then replays the log (`followRenames`, `loadRetired`, `renamedTo`, `moveRenamedChecks`) to learn what a name means now. | EMI-308: `tracks card` mid-rename failed with `no card` (11 of 1,200 writers in a probe). EMI-318 (open): `tracks bind` during a rename recreates the old card as a second track, 27 of 60 rounds with 8 concurrent binds; it predates EMI-308. 54 of the 1,860 events are renames. |
| P4 | **One flock plus atomic renames** (`withCardsLock`, `writeAtomic`, `writeExisting`, `linkNew`, `linkArchive`). Every writer must remember to take the lock and re-read under it; the hook gets 500 ms per write because Claude Code kills it at about 2 s. `checks.json` has its own lock and is written outside the cards lock. | EMI-294: a mode Emil set in the pane was lost to an agent's write from an older read. The fix was the lock; `tracks bind` still resolves outside it (EMI-318). |
| P5 | **File metadata carries meaning.** `ls` `updated` is the file mtime; the pane's mode writes reset the mtime afterwards (`rewriteCard` + `Chtimes`) so they do not mark the track unread; the pane's change signal stats 80 files every 500 ms and needs the inode because a same-size rewrite keeps size and (reset) mtime. | EMI-315: a pane write with the same size went unseen until the inode joined the stamp. |
| P6 | **Every writer re-parses and edits Markdown** by line surgery: `setSection`, `insertAsk`, `removeAsk`, `setMetaText`/`upsertMetaText`, `closeCard`, plus rules such as "no line may start with `## `" and "no value may contain ` # `" because the parser would misread them. | Roughly 1,000 of the 8,200 non-test lines are this surgery, locking and file moves. |
| P7 | **Stats and "what needs me" rescan text**: every hook call loads all cards to find the one with `$REX_SESSION`; `archivedCard` globs and reads the archive for every name check; `stats` and `seenSince` read the whole log. | No failure; fine at this size. Listed so nobody counts it as a reason. |

What SQLite fixes: P1-P6. Asks get an id, a kind and created/seen/closed times; a track gets an id that a rename does not change; a transaction replaces the lock and the re-read; `changed_at` and `updated_at` become columns; the change signal becomes a counter. Most of the text surgery and the rename machinery goes.

What it does not fix:

- **Rex races.** Sending an answer into a pane that changes state between text and Enter (`sendAnswer`) is about Rex, not storage.
- **Agents writing bad content**, or a stale agent that ignores the skill and writes the file by hand. After the cut-over such a write lands on a rendered view and is overwritten; section 3 says how that is caught.
- **The hook's time budget.** A write still waits when another writer holds the database, but writers hold it for milliseconds instead of a Markdown round trip.
- **Emil editing a card by hand** (Standing orders today has no command). That needs a command; see section 3.

What it costs: a dependency (about 6-10 MB more binary for `modernc.org/sqlite`), a schema to migrate, state you cannot `cat` (the rendered files cover reading, not writing), a single file whose corruption loses everything (daily `VACUUM INTO` backup), and one more change for Meridian's watch (avoided at first; see section 3).

## 2. Options

| Option | What it means | For | Against |
|---|---|---|---|
| **A. SQLite as source of truth, Markdown rendered** (recommended) | Tables for tracks, names, asks, checks, events. Writers run transactions; files under `cards/` are written from the database after each commit. | Fixes P1-P6 at the root. Deletes code rather than adding it. Rename becomes an `UPDATE`; "which ask" becomes an id. Readers that grep files keep working. | One migration of live state (tier 3). New dependency. Hand edits need a command. |
| **B. Markdown plus structured sidecars** (`asks.json`, `answers.json`, `names.json`) | Asks and their state move to JSON next to the card; the card shows them. | Smaller step; no new dependency. | Two files per fact that must agree, kept consistent by the same flock: P4 gets worse, not better. Renames now move several files. The agent's `asks:` list still has to be matched to sidecar entries by text. |
| **C. Keep files, fix the worst parts** | Resolve `bind` under the lock (EMI-318), drop or replace the old check ask on flip (EMI-318), keep `asked` across a `check:` change, stamp ask times in a hidden comment. | Cheapest; each fix is small and tier 2-3 on its own. | Each fix is another text rule over the same mechanisms; P1 and P3 remain, so the next probe finds the next race. The history (EMI-277, 294, 308, 315, 318 in five days) is this option already running. |

**Recommendation: A**, honestly hedged. At this size SQLite buys nothing in speed and the files are not "too big". If the tracks tool were frozen, C would be the right call: fix EMI-318 and stop. It is not frozen: Meridian, the pane, answers and checks keep adding state that needs identity (ask ids, seen times), and each addition costs a race probe today. If Emil prefers to defer, do C's EMI-318 fixes now (they are needed until cut-over anyway) and revisit A when the next ask-identity bug lands.

## 3. Design

### Schema sketch

```sql
PRAGMA journal_mode = WAL;  PRAGMA foreign_keys = ON;  PRAGMA user_version = 1;

CREATE TABLE tracks (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,              -- current name
  mode        TEXT NOT NULL DEFAULT 'focus',
  tier        TEXT NOT NULL DEFAULT '',   -- '', '1', '2', '3'
  tier_why    TEXT NOT NULL DEFAULT '',
  dir         TEXT NOT NULL DEFAULT '',
  rex_session TEXT NOT NULL DEFAULT '',
  claude_session TEXT NOT NULL DEFAULT '', claude_cwd TEXT NOT NULL DEFAULT '',
  check_text  TEXT NOT NULL DEFAULT '',   -- the check: line
  waiting_on  TEXT NOT NULL DEFAULT '',
  links       TEXT NOT NULL DEFAULT '',   -- as written; parsed on read like today
  goal        TEXT NOT NULL DEFAULT '',
  where_text  TEXT NOT NULL DEFAULT '',   -- Where it is (becomes Outcome when closed)
  standing    TEXT NOT NULL DEFAULT '',   -- Standing orders
  later       TEXT NOT NULL DEFAULT '',
  extra       TEXT NOT NULL DEFAULT '{}', -- JSON: unknown frontmatter keys and sections kept from import
  answered_at TEXT,                       -- hook: Emil typed while asks were open; cleared on Stop/SessionStart
  updated_at  TEXT NOT NULL,              -- content stamp: tracks card, tracks check adding an ask
  changed_at  TEXT NOT NULL,              -- any write (the pane's since)
  created_at  TEXT NOT NULL,
  closed_at   TEXT,                       -- NULL while open
  version     INTEGER NOT NULL DEFAULT 1, -- bumped per write
  render_hash TEXT NOT NULL DEFAULT ''    -- hash of the card file as last rendered; a different hash on disk is a hand edit
);
-- Every name a track has held. A name is never reused: live, closed or renamed away.
CREATE TABLE names (
  name     TEXT PRIMARY KEY COLLATE NOCASE,
  track_id INTEGER NOT NULL REFERENCES tracks(id),
  retired_at TEXT                          -- set when renamed away
);
CREATE TABLE asks (
  id        INTEGER PRIMARY KEY,
  track_id  INTEGER NOT NULL REFERENCES tracks(id),
  kind      TEXT NOT NULL,                -- 'agent' | 'check'
  text      TEXT NOT NULL,
  position  INTEGER NOT NULL,             -- display order among open asks; the number is derived
  check_id  INTEGER REFERENCES checks(id),
  created_at TEXT NOT NULL,
  seen_at   TEXT,                         -- first open/answer/prompt after created_at
  answered_at TEXT,                       -- pane sent an answer that accepted this ask
  closed_at TEXT, closed_by TEXT          -- 'agent' | 'applied' | 'superseded' | 'closed'
);
CREATE TABLE checks (                     -- replaces checks.json
  id INTEGER PRIMARY KEY, track_id INTEGER NOT NULL REFERENCES tracks(id),
  text TEXT NOT NULL, answer INTEGER NOT NULL, asked_at TEXT, ran_at TEXT,
  UNIQUE (track_id, text)
);
CREATE TABLE events (                     -- replaces attention.jsonl; same fields
  id INTEGER PRIMARY KEY, at TEXT NOT NULL, event TEXT NOT NULL,
  track_id INTEGER REFERENCES tracks(id), track_name TEXT NOT NULL DEFAULT '',  -- name at the time
  rex_session TEXT, rex_block TEXT, cwd TEXT, len INTEGER, kind TEXT
);
CREATE INDEX events_track_at ON events(track_id, at);
```

`extra` keeps whatever a card holds that the schema does not name, so import loses nothing and rendering gives it back.

### Who writes what, one transaction each

All writers use `BEGIN IMMEDIATE` with `busy_timeout` 5 s (500 ms in the hook; behind a longer writer the hook tries its whole transaction again without rendering the view, and logs the event alone only in the last 400 ms of its 1.8 s budget). Rule: no Rex or `gh` call inside a transaction.

| Writer | Transaction |
|---|---|
| `tracks bind NAME` | resolve NAME through `names` (follows renames; closed reports `closed`); insert the track and its name if none; set `rex_session`. Fixes the EMI-318 bind race by construction. |
| `tracks card NAME` | resolve; update the fields given; reconcile agent asks (below); set `updated_at`, `changed_at`. |
| `tracks new`, pane `n` | insert track + name; the `names` primary key refuses a taken, closed or retired name, case-insensitively (replaces `archivedCard`, `loadRetired`, `nameTaken`). |
| `tracks rename OLD NEW` | insert NEW into `names`, set OLD's `retired_at`, update `tracks.name`, insert a `rename` event. Old events keep `track_id`, so no replay. |
| `tracks close` | set `closed_at`, close open asks (`closed_by='closed'`), event. Session handling stays after the commit. |
| `tracks check` | `gh` outside; then per track: re-read `check_text` and mode, upsert `checks`, and on a new or flipped answer close the old check ask (`superseded`) and insert the new one, plus a `check` event. Fixes EMI-308/318 check-ask cases. `checks.lock` stays as the "one run at a time" guard. |
| pane mode keys | update `mode`, `changed_at` (not `updated_at`), `mode` event. No `Chtimes`. |
| pane apply check ask | by ask id: close it (`applied`), set mode if recommended, `answer` event. |
| pane send answer | after Enter: set `answered_at` on the accepted ask ids, `answer` event (replaces `answered_asks:`). |
| `tracks hook` | insert the event; on prompt with open asks set `tracks.answered_at`; on Stop/SessionStart clear it; record `claude_session`. One short transaction instead of event-then-card. |
| resume, adopt | update `rex_session`, `claude_*`. |
| any open/answer/prompt event | also sets `seen_at` on that track's open asks created before it (replaces `seenSince` over the whole log). |

**Agent ask reconciliation.** `asks:` keeps the agent's order. An agent ask whose text matches an open one keeps its id and `created_at`; open agent asks not listed are closed (`closed_by='agent'`); open check asks with `seen_at IS NULL` stay, listed after the agent's (today's rule, now a column instead of `checkAskAnswered`). Numbers stay positional, so `ls --json`, the pane and the agent see the same `1.`, `2.`.

### Change signal

- **Pane:** keep its connection open and read `PRAGMA data_version` every 500 ms; it changes on any other connection's commit. Replaces `fileStamp`.
- **Meridian (phase 1, no change):** after each commit the writer renders the touched card to `cards/NAME.md` (atomic rename), so the FSEvents watch on `cards/` fires as today, including for hook and mode writes.
- **Meridian (later, optional):** a `$TRACKS_HOME/changed` file holding a counter, written after each commit, if rendering ever stops being per-write. Not needed for this issue.

### Reading and grepping a card

- `cards/NAME.md` and `cards/archive/DATE-NAME.md` are rendered after each commit, in today's format (frontmatter keys and comments as `cardText` writes them), plus one frontmatter line `generated: tracks.db (write with tracks card)`. `tracks open NAME`, `tracks bind` and `grep -r` work unchanged. The 69 existing archive files are left as they are.
- Rendering is not part of the transaction: it runs after the commit, under the write lock, from the committed rows. If it fails, the database is still right, the failure is kept in `$TRACKS_HOME/render.err` and the pane footer shows it, and `tracks render` (also run at the end of `tracks check`) rewrites every view; a full render that succeeds removes `render.err`.
- **A hand edit to a view** is detected at the next render: the file's hash differs from the last one rendered (kept in the database). The edited file is kept as `cards/.edited-NAME-TIME.md`, the view is re-rendered, and the pane footer says so. It is never imported automatically: that would make the Markdown a protocol again.
- **No `tracks edit`.** Emil does not edit cards by hand (2026-11-20), so there is no hand path; the detection above is the only guard.

### CLI contracts that must not change

- `tracks bind NAME`: prints the card text, or `closed`, or `renamed to NEW`.
- `tracks card NAME < fields`: same input grammar and refusals (keep the `## ` and ` # ` refusals so views stay parseable), same output: numbered asks, `no asks`, `closed`, `renamed to NEW`.
- `tracks ls --json`: schema 1, every key present, same types. `asks[].since` becomes the ask's real `created_at` (more accurate, same type); `updated` is the track's last content change, as a card file's mtime was: `updated_at` once something wrote the content, else the imported card file's mtime. A mode key, the hook's bookkeeping and the pane's answer records move only `changed_at`, so they leave `updated` alone (a mode key kept the mtime on the files; the hook's stamp did not, and now does). The import takes `updated_at` from the card's `updated:` stamp, but never later than the file's mtime. No new keys in this work: Meridian's decoder (TracksContract.swift) is the reference, and a new key is its own Meridian change.
- `tracks check`, `tracks stats`, `tracks resume`, `tracks hook`, `tracks next`, `tracks archive`, `tracks close`, `tracks rename`, `tracks new`: same arguments, output and exit codes.
- `TRACKS_HOME` still relocates everything; tests keep running against a temp home.

## 4. Migration and rollback

**Import (`tracks migrate`)**

1. Refuse while a pane runs on this home (`pgrep` for a process started as `tracks`, `tracks --float` or `tracks --home`, whose `TRACKS_HOME`, or `HOME`, read with `ps -E`, is this home; a pane whose environment cannot be read counts): a pane started from the old binary would keep writing files that are now views. Say "quit the pane, then run again".
2. Take `checks.lock`, `cards.lock` and `events.lock`, in that order (the order `tracks check` takes them), and hold them until every view is rendered. Every writer on the files takes `cards.lock` and looks for `tracks.db` again once it holds it; every append to `attention.jsonl` does the same under `events.lock`; `tracks check` does it under `checks.lock`. A writer that finds `tracks.db` there writes to it instead, so nothing lands on the files between the import and the switch. A writer from a binary older than slice 4 does not take `events.lock`: install the new binary before migrating.
3. Build `tracks.db.new` from scratch: live cards, archived cards (name from `name:`, else the file name; `closed_at` from `closed:`, else mtime), `checks.json`, then `attention.jsonl` in order. Rename events build `names` and set `track_id` on earlier events (the same walk as `followRenames`, done once). Asks get `created_at` = the card's mtime and `seen_at` from `seenSince`, since nothing better exists.
4. **Verify before switching:** render every card from `tracks.db.new` and compare with the source card parsed by `parseCard` (same meta values and sections; whitespace normalised; `updated:` no later than the file's mtime). Compare `tracks ls --json` from files and from the database, ignoring `generated_at` and `since`. Any difference deletes `tracks.db.new` and aborts, leaving the files as they were.
5. Back up: archive the whole `$TRACKS_HOME` (without the backup folder and `tracks.db.new`) to `$TRACKS_HOME/backup/pre-store-TIME.tar.gz`, read it back, and verify it lists every file with its size. It runs after the verify so that an abort leaves nothing behind.
6. Rename `tracks.db.new` to `tracks.db` (this is the switch), rename `checks.json` and `attention.jsonl` to `*.imported`, and render every view, including cards the import read and nothing wrote since, so each carries the generated line and its own `render_hash`. (`tracks render` and `tracks check` leave such a card's file as it is.)

Idempotent: with `tracks.db` present, `tracks migrate` prints "already migrated" and does nothing. A failed run leaves only `tracks.db.new`, which the next run deletes and rebuilds. `tracks migrate --check` runs steps 3-4 into a temp file and touches nothing; it can run against live state for days before the switch.

**Cut-over, not dual-write.** The new binary reads `tracks.db` when it exists and the files when it does not, so installing it changes nothing until `tracks migrate` runs. Dual-write was rejected: two sources of truth is the problem being solved, and it doubles every race.

**Rollback (`tracks export`)**

Writes the database back to the layout the previous release reads: all views rendered (already there), `checks.json` from `checks`, and `attention.jsonl` rebuilt from `events` (rename events included, so the old replay still works). It then moves `tracks.db` aside to `tracks.db.rolled-back-TIME`. Reinstall the previous tag (recorded as `PRE_STORE_TAG` in the cut-over PR) and restart the pane. Answered and seen state collapses back to `answered:` and event-derived rules, which is what the old binary computes anyway. Last resort: untar the backup, losing changes since the cut-over.

**After the switch:** `tracks check` runs `VACUUM INTO backup/tracks-DATE.db` once a day and keeps 14.

## 5. Steps

Each is one PR. The first is useful on its own and needed whatever is decided.

| # | Slice | Tier | Acceptance |
|---|---|---|---|
| 0 | **EMI-318 on files**: `bind` resolves under the lock; a flipped check supersedes its old ask. | 3 (writes cards from the hook path and launchd) | The EMI-318 probe (8 binds against one rename, 60 rounds) yields 0 duplicate cards; a yes-to-no flip leaves only the "no" ask. |
| 1 | **Store package, no callers**: `internal/store` with schema, open (WAL, busy timeout, 0600), the transactions of section 3 as functions, and the renderer to today's card format. | 2 | Unit tests per transaction; a concurrency test with 8 writers (card, mode, hook, check) on one track keeps every change; render of a fixture equals the fixture `cardText` would write. |
| 2 | **`tracks migrate --check` and `tracks export`**, read-only against live state. | 2 | On a copy of the real `~/.local/state/tracks`: import, render and `ls --json` match the files; export then the previous release's `ls --json`, `archive` and `stats --days 30` print the same as before import. Run `--check` daily until slice 4. |
| 3 | **All commands on the store behind the switch** (database present, else files): bind, card, new, rename, close, check, hook, resume, pane writes, `ls`, `stats`, `archive`, `next`; pane reload on `data_version`; per-commit render; hand-edit detection. | 3 (hook, check, pane typing paths) | Full test suite passes in both modes; the EMI-294 and EMI-318 probes pass against the database; a hook call with another writer holding the database for 400 ms still logs its event under 1 s; Meridian (unchanged) refreshes within 2 s of a `tracks card` write. |
| 4 | **Cut-over on Emil's machine**: install, quit pane, `tracks migrate`, restart pane. Update SKILL.md line 10 (the card is a view; write it only with the two commands). | 3 (migrates live state) | Backup present; `ls --json` before and after differ only in `generated_at` and `since`; one full day of tracks with the pane, a launchd check that adds an ask, a resume on Rex restart, and a rename, with no `.edited-*` file and no lost write. Rollback rehearsed once on a copy first. |
| 5 | **Remove the file code paths** after a week without rollback: `withCardsLock`, `insertAsk`, `removeAsk`, `setMetaText` writers, rename hard-link machinery, `fileStamp`, `checks.json` I/O. | 2 | Suite green; `rg 'withCardsLock|writeExisting|followRenames'` finds only the import code. |

## Schema 8: sessions, questions, messages (EMI-376)

The v3 primitives of `docs/desk-v3.md`, added beside the old tables, which stay and are still written: nothing reads the new ones yet except `tracks sessions`.

- **sessions**: one row per Claude Code conversation, keyed by Claude's session id: track (or none), parent session, `owner` (emil, desk), `host` (rex, background, stopped), name, cwd, model, `status` (idle, working, done, blocked, error), `waiting_for`, `last_answer`, `seen_at`, created, updated and ended times. `tracks hook` writes it on SessionStart, UserPromptSubmit, Stop, Notification, PermissionRequest, SessionEnd, PreToolUse, SubagentStart and SubagentStop (the last three give the row `activity` and its `session_subagents`, schema 14; see `tracks hook` in keys.md); `tracks sessions` marks a row stopped when `claude agents --json` no longer lists it.
- **questions**: track and session (both optional), `from_kind` (agent, desk, check), the question, `options` (JSON `[{label, description}]`, at most 9), `recommended` (an option index), `evidence` (JSON `[{text, source}]`), links, and the answer (option or text, by whom, when, delivered, closed). Empty.
- **messages**: text for a session, `from_kind` (emil, desk, session, system) and `from_session`, `kind` (note, answer, report), `held`, `delivered_at`. Empty; the hook already reads it for the owner hand-back.

The migration from 7 runs when a tracks command first opens the database (`store.Open`), under an flock on `migrate.lock` in the home, never on the database itself: closing any descriptor of a SQLite file drops this process's locks on it. A process that finds the lock held carries on with schema 7 for that run (the hook writes no session row and stays quiet); a new database waits for it. Before the first schema 8 write, the process holding the lock copies the database with SQLite (`VACUUM INTO backup/pre-migrate-v7-TIME.db`, outside any transaction), checks the copy with `integrity_check`, and archives the rest of the home to `backup/pre-migrate-v7-TIME.tar.gz`, which `tracks migrate`'s backup code writes and verifies. The archive leaves out `tracks.db` and its `-wal`, `-shm` and `-journal` files, and its check skips `hook.ok`, `hook.err` and `render.err`, which a hook may rewrite meanwhile. A failed backup stops the migration, and the database stays at 7. The migration backfills one sessions row (owner emil, host rex) per open track with a `claude_session`.

A binary from before schema 8 refuses the database ("schema is version 8"). To roll back: quit the pane, install the previous binary, then either set `PRAGMA user_version = 7` (`sqlite3 tracks.db 'PRAGMA user_version = 7'`; the new tables are additive and old code ignores them), or go back to the state before the migration by moving `tracks.db`, `tracks.db-wal` and `tracks.db-shm` aside, copying `backup/pre-migrate-v7-TIME.db` to `tracks.db`, and untarring the archive over the home for the card views (it holds no database file). That second way loses every write since the migration.

## Schema 9: the desk's start queue (EMI-377)

- **desk_queue**: a start the desk asked for while it had `TRACKS_MAX_DESK` sessions working or blocked: the track (its card is made at once), the parent session, the model, the prompt, `queued_at`, and `started_at` once `tracks wait --desk` took it. A track deleted because its session never started takes its queue rows with it.

The migration from 8 runs as the one to 8 does: under `migrate.lock`, after the backup (`backup/pre-migrate-v8-TIME.db` and its archive). It only adds the table. To roll back, install the previous binary and set `PRAGMA user_version = 8`; the old code ignores the table.

## Schema 10: questions mirror the cards' asks (EMI-379)

Two columns on `questions`: `ask_id`, the card ask a question mirrors (null for a question raised by `tracks decision add`), and `recommendation`, the recommendation in words. Every `tracks card`, `AddAsk` and track close writes the track's open agent asks into `questions` (from `agent`; options read from a numbered `1) a 2) b` list only; the recommended option only when `Recommend:` holds exactly one number and it is one of them) and closes the question of an ask that is gone. An ask keeps its question, so its `D` id, for as long as it keeps its row, which a rewrite with the same text does. While another process migrates, nothing is mirrored.

The migration mirrors the asks already open, so the band is not empty until agents rewrite their cards. A database at 8 or 9 goes to 10 in one open, under `migrate.lock` and after one backup (`backup/pre-migrate-vN-TIME.db` and its archive, N the version it was at). To roll back, install the previous binary and set `PRAGMA user_version = 9`; the old code ignores the new columns.

## Schema 11: the headless desk's conversation (EMI-380)

A `conversation` table holds the desk's chat as `prism server` runs it: Emil's messages (`role emil`, `delivered_at` set once written to the desk, so a restarted serve sends the rest), the desk's replies (`role desk`, its text and its tool calls counted in `steps`, updated while the turn runs) and its sessions' reports (`role report`, with `session_id` and `outcome`). A `claude_session` column on `desk` keeps the headless desk's Claude session for `--resume`, and serve writes that session's `sessions` row (owner desk, no track, named desk) itself, since hooks skip a stream-json session.

A database at 8, 9 or 10 goes to 11 in one open, under `migrate.lock` and after one backup, as before. To roll back, install the previous binary and set `PRAGMA user_version = 10`; the old code ignores the new table and column.

## Schema 15: standing orders (EMI-419)

A `desk_orders` table holds Emil's standing orders (`tracks orders add|drop`): `text`, `created_at`, and `dropped_at` once lifted. `tracks bind` prints the ones in force under the card, and every note the desk delivers carries them, so a session the desk starts knows them without the desk's memory. A desk entry in `conversation` that `prism server` hid for numbering questions in chat (EMI-413) has `outcome` `reasked`; that needs no schema change. A question with no track (`tracks decision add --desk`) keeps `track_id` NULL, which the column always allowed.

To roll back, install the previous binary and set `PRAGMA user_version = 14`; the old code ignores the table.

## 6. Decisions (2026-11-20)

1. **Slice 0 now, then SQLite (option A).** Emil: yes.
2. **Driver: `modernc.org/sqlite`.** Emil asked for the best current option. modernc is the most used pure-Go driver, needs no cgo, follows upstream SQLite releases and costs about 6-10 MB of binary. `ncruces/go-sqlite3` (SQLite compiled to WASM, run by wazero) is the newer approach and smaller, but has a shorter track record; nothing at this size needs what it adds. The store package hides the driver, so switching later is one import.
3. **No hand edits, so no `tracks edit`.** Emil has never edited a card by hand. Hand edits to a view are detected and set aside, never imported.
4. **Events move into the database**, the recommendation: seen-state and renames need `track_id`, and the hook's event and card write become one transaction.
5. **A rendered file per live card on every write**, the recommendation: Meridian's watch and grep work with no change.
6. **`ls --json` `updated` keeps today's meaning**: the last content change, which is what the card file's mtime said once the pane's mode writes kept it. The hook's bookkeeping no longer moves it either. A separate "last write of any kind" key only if Meridian asks for it, as its own schema change.

Decisions 4-6 follow the recommendations; Emil did not answer them separately.

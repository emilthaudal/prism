# Prism (macOS)

Prism is the desk's native window (renamed from Desk on 2026-11-25; the CLI keeps `tracks desk` for now) (docs/desk-v3.md, step 6): sessions | conversation | board, a client of `prism server`
(docs/serve-api.md) and nothing else.

- SwiftPM only, no Xcode project, no third-party dependencies, laid out like ~/Developer/lumen. `DeskCore` holds the
  models, the API client, the fixture client and all logic (sync, board, sidebar, keys, the answer countdown); `Prism` is
  the SwiftUI/AppKit shell.
- `make test` must pass. `make app` builds and signs `build/Prism.app`; `make install` copies it to `~/Applications`.
- `Prism --fixture ../mock/serve-fixture.json` (`make demo`) runs on the fixture with actions applied in memory; tests and
  shots never touch `prism server`, `tracks.db` or Rex.
- `make shots` captures the window from the fixture into docs/ in the background: `open -g`, `--background` (no Dock
  icon, never active, no notifications), `screencapture -l` by window id, quit. Never activate the app, steal focus or
  drive the UI on Emil's Mac.
- `make motion` plays `scripts/motion-sample.json` on the fixture, films the window in the background with
  ScreenCaptureKit and fails on a step that hitches, plays twice or shows doubles (docs/motion.md). Run it before and
  after a motion change.
- `make motion-composer` plays loading, answering, Undo and sending a decision in the composer (EMI-461).
- `make motion-arrive` plays the arrival and leaving scenarios on a longer fixture (docs/motion.md); `Motion` in Theme.swift holds the
  animation tokens and nothing else in `Sources/Prism` should write an animation literal.
- Without a Screen Recording grant, `--snapshot PNG` draws the window in-process and quits. Glass doesn't draw that way
  and neither does the split view's sidebar, so `--sidebar-only` (fixture only) hosts the sidebar alone for a capture;
  `--demo-pick N` stages option N of the `--select`ed question.
- The app icon is `Resources/Prism.icon` (macOS 26 Icon Composer format), compiled by `actool` in `make app`; Xcode
  must be installed. `make icon` redraws its art from `scripts/icon.mjs`. A plain `.icns` would sit in macOS 26's grey
  tile.
- `make ui-check` plays `scripts/ui-sample.json` on the fixture, sets hover, press, focus and selection inside the app, takes a
  still per step in the background and compares it with `ui/baselines`, which are local and not in git: `make ui-baseline` before a change, `make ui-check` after it (docs/motion.md).
- `make stress` writes worst-case fixtures into build/stress (scripts/stress-fixture.py): `big` (40 tracks, 120 sessions,
  30 open questions, 2000 messages, plus `replay.json`, a report every half second), `worst` (long, one-letter, emoji and
  non-Latin names, huge counts, missing fields) and `empty`. An idle window should sit near 0 % CPU on `big` (`top -l`).
- One spacing scale (`Space` in Theme.swift), native controls first, text never closer to a shape's edge than its corner
  radius.

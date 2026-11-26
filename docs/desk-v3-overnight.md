# Desk v3: what was built overnight (2026-11-24/25)

Everything below is on the `desk-v3` branch (worktree `.claude/worktrees/desk-v3`). It isn't merged to `main` or installed. `make test` passes on it (deskui, store, tracks).

Installed since yesterday: only step 1 (schema 8, `tracks sessions`). The live `tracks.db` is at schema 8.

## What's built

| Step | Ticket | What | Review |
|---|---|---|---|
| 1 | EMI-376 | Sessions, Questions and Messages tables; status from hooks; `tracks sessions` | 1 round, fixed (backup lock loss). **Installed.** |
| 2 | EMI-377 | The desk starts its sessions with `claude --bg` (owner desk), cap 4 with a queue, Stop reports to the desk, `tracks wait --desk` | 1 round, fixed (queue started a session on a track you'd picked up) |
| 3 | EMI-378 | `tracks send` holds notes for busy desk sessions and delivers them at Stop; permission prompts of desk sessions become questions | 1 round, fixed (notes starting with `-`; desk answering your session; hidden session rules) |
| 4 | EMI-379 | Questions as records (`tracks decision add/close`, `decide`, `decisions`), card asks mirrored with stable D ids, the pane band answers with one key and a 5 s undo (`a` focuses it) | 1 round, fixed (desk could answer your tracks; parser misread 2 of 11 live asks) |
| 5 | EMI-380 | `tracks serve`: the local API (docs/serve-api.md) and the headless desk (stream-json), events held until the desk's turn ends | 1 round, fixed (who can act as the desk; forged "Emil:" lines) |
| 6 | EMI-381 | The native app, `mac/` (SwiftUI, signed like Meridian) | self-checked |
| B | EMI-382 | `tracks ui`, the three-column TUI | self-checked |
| 7 | | Deleting what v3 replaced | not started: after a week of use, as planned |

Schema on the branch: 11 (8 sessions/questions/messages, 9 desk_queue, 10 question keys, 11 the desk's conversation). The first open of the live database migrates 8 → 11 in one go, with a verified backup first.

## Verified end to end

- **Native app against a real server:** with `tracks serve` running Haiku as the desk in a throwaway home, the app showed the desk's first status. A message typed through the API got "hello from the desk" back. Screenshot: `/private/tmp/claude-501/-Users-emtb-Developer/7fe2cf3d-a8cf-4e6c-97b7-128e454651dd/scratchpad/smoke/live-desk.png`. The fixture view against `serve --mock`: `…/smoke/live.png`.
- **Steps 2 and 3 with real Haiku `--bg` sessions:** a session's report reached the desk; a note sent while it worked was delivered after its Stop; a note starting with `- ` woke the same session; a permission prompt became D1, and answering it let the session go on.
- **Every review ran against a copy of your live home.** Migration timing was about 400 ms, and hooks took 20-130 ms.

## Try it (nothing here touches your live state)

```sh
cd ~/Developer/tracks/.claude/worktrees/desk-v3
go build -o /tmp/tracks-v3 ./cmd/tracks
/tmp/tracks-v3 ui --fixture mock/serve-fixture.json          # option B in a terminal
make -C mac app && make -C mac demo                          # the native app on the fixture
```

## To install (your call, it changes what you use)

1. Merge `desk-v3` into `main`. The skills are linked to the main checkout, so the desk skill changes at once. Close the interactive desk first, or it runs the new text mid-session.
2. `make install`. The next hook migrates `tracks.db` 8 → 11 with a backup.
3. `make install-skills`.
4. `make -C mac install` (builds and copies `Prism.app`, formerly `Desk.app`, to `~/Applications`).
5. `tracks serve`. It refuses while an interactive desk runs (`--force` takes over). Nothing starts it at login yet.

## Decisions I made overnight (reverse any)

- Native app (EMI-381) merged to desk-v3: answer option numbers count from 0 (matches `recommended`); notifications built but never run (need the signed app in front and your permission); no actions sent over real HTTP yet.
- Step 2 (EMI-377): background sessions inherit the env of whichever process started Claude's bg service, so desk session settings now force empty REX_SESSION and the right PATH/TRACKS_HOME. Queue is a new desk_queue table (schema 9, same backup path).
- Step 2 open: a verify-hook send-back still produces a "done" report mid-work; a crashed bg session holds a cap slot until tracks sessions marks it stopped; `u` on a background start closes the card but does not stop the session.
- B TUI (EMI-382): `tracks ui` (run `tracks ui --fixture mock/serve-fixture.json` to try it now). Tab cycles columns; ↑↓ walks questions; in the composer options are alt+1-9; ctrl+z always undoes. Spinner is static. Window adds its own "You answered…" lines: if serve also posts them they duplicate.
- Step 4 (EMI-379): a reworded ask becomes a new D id (same text keeps it); "yes/no"-style slash lists are parsed as options, so a path in an ask can be misread as options; band keys act only with the band focused (a, or tab onto a track with a question) because enter already opens a track.
- Step 4 review: answers from the desk on your tracks are refused outright now (v3: the desk never answers your tracks); slash-list option parsing removed after it misread 2 of 11 live asks.
- Open: `tracks decide --by emil` is not structurally guarded; only the desk skill stops the desk from passing an answer off as yours. A structural guard (refuse --by emil from the desk session) is cheap if you want it.
- Step 3 (EMI-378): "Allow for this session" lasts only until the next delivered note, because delivery stops and resumes the session (session-only rules are lost). Fix later by passing allowed rules on resume, or accept. An answer in words with no option counts as a deny.
- Decision made overnight (step 3 review): the desk may answer a permission prompt itself only with "Allow once" or "Deny", and only on tier 1-2 tracks; "Allow for this session" (now showing its exact rule, e.g. Bash(gh pr:*)) and anything on tier 3 always come to you.
- Step 5 (EMI-380): `tracks serve` takes over the desk binding while it runs (an interactive Rex desk loses desk rights). The headless desk loads the desk skill from ~/.claude/skills/desk, which is linked to the main checkout: the new headless skill text only takes effect after desk-v3 is merged to main (make install-skills). Reports are marked delivered when serve reads them; if serve dies before the desk sees them they are lost (your messages are not).
- Verified overnight: the native app (built from emi-381) connected to a real `tracks serve --mock` over HTTP and rendered the snapshot (no "fixture" label, ages from the real clock). Screenshot: /private/tmp/claude-501/-Users-emtb-Developer/7fe2cf3d-a8cf-4e6c-97b7-128e454651dd/scratchpad/smoke/live.png
- The desk may answer a permission prompt itself only with "Allow once" or "Deny", only on tier 1-2 tracks it owns. "Allow for this session" (which now shows its exact rule) and anything on tier 3 come to you.
- Answer option numbers count from 0 in the API. Both clients and the server agree on that.
- `tracks serve` takes the desk binding. The desk proves itself with a secret that `serve` makes each time it starts the desk.

## Open for you

1. **Native app or B TUI?** Try both and pick. They share the API, so the other can stay or go.
2. **Install now?** See the steps above.
3. **`tracks serve` at login** (launchd) once you've picked: a small step.
4. **Session-only permission rules** are lost on the next delivered note. Accept that, or pass the rules on resume.
5. **The headless desk with Opus** hasn't run yet (only Haiku), and neither has the new headless skill text (the probe loaded the installed old skill).

## Not verified

- A real takeover that opens a Rex tab (`t`), and giving a session back (`g`), through the app or the TUI.
- macOS notifications from the app: they need the app in front and your permission.
- Diff stats on real branches, the transcript route on a real session, and `stop`/`open` against real sessions.
- The 240 s permission timeout with a real session.

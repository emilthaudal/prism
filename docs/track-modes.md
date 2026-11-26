# Track modes: why everything ends up in focus, and what to do

EMI-423, 26 Nov 2026. A proposal: nothing here is built yet.

## What happens today

Every way to make a track puts it in focus:

- `tracks start` (the desk) and `newNamedTrack`: `internal/tracks/start.go:271`, `internal/tracks/desk_acts.go:283` and `:297` hard-code `"focus"`.
- `tracks new` defaults its `--mode` flag to focus (`internal/tracks/cmds.go:407`), and the store does the same when no mode is given (`internal/store/tx.go:54`).

Nothing moves a track out again. `waiting` is set only by hand, and the only automatic mode change is the other way: a `check:` that turns yes on a waiting track asks "set mode to focus" (`internal/store/tx.go:398`). The limit of 2 is a red header in the pane (`FOCUS  4, plan says 2`) that Prism doesn't show at all. The morning chat is the only thing that trims focus, and on 25 Nov it didn't run.

On 26 Nov at 00:05, 7 of 9 open tracks were in focus, among them pan-402 (waits on a review) and review-acceptance-check-first.

What a mode changes today:

| | focus | background | waiting | parked |
|---|---|---|---|---|
| Pane | first group; header warns past 2 | second group | third group, dimmed | last group |
| Prism sidebar | section | section | section, dimmed | section, folded |
| Prism notifications | same for all: every desk question notifies, with sound | same | same | same |
| `tracks next` / need order | first | second | third | never needs Emil |
| Desk | morning chat, `today:`, may interrupt for it | — | not nudged when idle | not resumed, not checked |
| `tracks check` | runs | runs | runs; a yes suggests focus | skipped |

So focus costs nothing to enter and buys little: an order in a list. That is why it fills up.

## Emil's decisions (26 Nov)

1. A new track's mode is **picked by the desk at start**, not a fixed default.
2. Waiting is **computed** from `check:` / `waiting_on:` with no open asks and no working session.
3. A third focus track **asks which focus track moves to background**.
4. Notifications **follow the mode**: focus with sound, background a silent banner, waiting and parked in the queue only.

Points 1 and 3 below and slices 1 and 3 are updated to match; point 4 and slice 4 stand as written.

## Recommendation

Make focus scarce and Emil's, make waiting something tracks compute rather than something anyone sets, and make the modes change what reaches Emil.

1. **The desk picks the mode at start.** `tracks start --mode M` with M in focus, background or parked; the desk chooses from what Emil said and today's focus (focus when he asks for it now or it is today's priority, background otherwise). Without `--mode`, a track starts in background, so the pane's `n`, `tracks new` and any path that doesn't choose no longer lands in focus. A desk pick of focus when two are already there goes through the swap in point 3.
2. **Waiting is derived, not set.** A track shows as waiting when it has a `check:` or `waiting_on:`, no open asks and no session working on it. When its check fires (an ask appears) or a session picks it up again, it shows in its own mode again. No one has to move it either way, so it can't go stale. The stored `waiting` mode reads as background from then on. A focus track that waits stays in focus but shows the wait on its row, because Emil chose it for today.
3. **Focus is capped at 2 with a swap, not a refusal.** Setting a third track to focus in Prism or the pane asks which focus track moves to background (both shown, the older one picked). The desk never puts a third track in focus on its own.
4. **Modes decide notifications.** Focus: banner with sound. Background: silent banner. Waiting and parked: no banner; the question still lands in the queue. This is the thing that makes focus worth choosing.
5. **Three modes Emil sets** (focus, background, parked) **plus a computed Waiting section.** The sidebar keeps four groups, but Emil only ever chooses among three.

## Slices

Each is small and fix-forward, in this order. Slices 1 to 3 write `tracks.db` (a default value, no schema change), so they take one read-only review round under this repo's CLAUDE.md.

1. **The desk picks the mode.** Change the four defaults above to background; add `--mode` to `tracks start` (and the desk's named start); desk skill: always pass `--mode`, with the rule in point 1 and one line to Emil naming the mode it picked. Check: `tracks start` in a scratch `TRACKS_HOME` makes a background card without `--mode` and a focus card with `--mode focus`; existing tests updated.
2. **Derived waiting.** One function, `effectiveMode(card, session state)`, used by the pane's `groupOf`, `tracks ls`, the need order and the serve API's track payload (new field `shown_mode`, so Prism needs only to group by it). Treat stored `waiting` as background. The check ask on a yes becomes "open the track". Check: unit tests for the four cases (check set / ask open / session working / none).
3. **Focus swap.** Prism's Mode menu and the pane's `f` ask which focus track leaves when two are already there. From the CLI (the desk's `tracks mode X focus` or `tracks start --mode focus`) it refuses with the two focus tracks named, so the desk puts the swap to Emil as a question instead of picking. Check: model test in DeskCore and a pane test.
4. **Notifications by mode.** `Notifier.post` takes the track's shown mode: sound for focus, silent banner for background, none for waiting and parked. Check: DeskCore test on the decision; a look in the app.
5. **Morning without the chat (optional, after a week of 1-4).** If focus still drifts, the first Prism open of the day puts "Today's focus? 1) A and B 2) …" in the queue, instead of waiting for Emil to talk to the desk.

## Not in this proposal

- Renaming `background`: it collides with "background session" (a desk session with no Rex pane). Worth a later look once the modes settle.
- Moving today's 7 focus tracks: the desk's next morning chat does that once slice 1 is in.

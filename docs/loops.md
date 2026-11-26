# Overnight loops (EMI-466)

A loop is a session the desk starts to work on one goal unattended, while Emil sleeps: it makes one improvement a turn, measures it, commits it on its branch and starts the next turn on its own, inside limits Emil set, until it says nothing worth doing is left.

## Commands

- `tracks start 'request' --loop [--until HH:MM | --for 8h] [--turns N] [--budget PCT]`: desk only. Defaults: 8 hours, 20 turns, a budget of 70% of the five-hour usage window, model sonnet unless `--model`, mode background. `--until` is the next time the clock reads HH:MM.
- `tracks loop`: one line per loop: `TRACK  turn K/N  until HH:MM  running | paused until HH:MM | ended: why`.
- `tracks loop stop NAME`: ends the loop after its current turn (a paused loop at the watch's next look). The session is not stopped.
- The serve snapshot's session carries `loop` (docs/serve-api.md).

## How a loop runs

The session's first prompt is `/track NAME` and the loop's rules: each turn is one iteration; append a line with what changed and the measurement to `loop-log.md` in its folder; never ask Emil and never wait, decide and note it under "Decisions for Emil" in that log; never merge, push, deploy or install; end each turn with `LOOP: continue - <next step>` or `LOOP: done - <why>`.

At each stop the desk's watch (inside `prism server`, or `tracks wait --desk`) decides by itself, spending no desk tokens:

1. Ends the loop when the answer's last `LOOP:` line says done, a stop was asked, all turns ran, or the deadline passed.
2. Pauses it when usage.json says the five-hour usage is at or over the budget, or the seven-day usage at or over 95%, until that window resets; it ends instead when the reset falls after the deadline. Usage older than an hour, or a window whose reset passed, counts as unknown and never pauses.
3. Otherwise sends the next turn as a note: `Continue the loop: iteration K of N, until HH:MM. <the rules in one line>`, through the same path as `tracks send` (held while the session works, compacted first past 250k tokens).

A turn that ends in an error (the reaper's cut-off turns, including a usage-limit cut once the limit resets) is tried again with the same iteration, up to 3 times in a row; the fourth ends the loop. A usage-limit block of a loop session is not reported to the desk.

The desk hears of a loop once: its usual `done` event when the loop ends, with the text starting `[loop ended after K of N turns: why]`. The handled log gets a `noted` entry when a loop starts and when it ends, none per turn.

## Safety

- **Bounded.** Turns, a deadline and a usage budget all hold; the first one reached ends or pauses the loop.
- **Nothing waits on a sleeping person.** A permission prompt or an MCP approval of a loop session is denied after 2 minutes unanswered, with text telling the session Emil is away and to find a way inside its folder that needs no new permission or skip the step and log it. Nothing is ever allowed automatically; the desk and Emil can still answer within those 2 minutes.
- **Same stops as any desk session.** The loop rules forbid merges, pushes, deploys and installs, and the session runs with the settings and hooks of every desk session.
- **Emil wins.** Typing into the session takes it over and ends the loop; `tracks loop stop` ends it after the current turn.
- **No schema change.** Loop state is `$TRACKS_HOME/loops/TRACK.json`; a stop request is `TRACK.stop` beside it, so it never races the watch's writes.

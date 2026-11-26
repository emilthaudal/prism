# Motion checks (`make motion`)

`make motion` plays a scripted scenario on the fixture, films Prism's window in the background and judges each step.
An agent runs it before and after a motion change and compares the lines.

```
make motion                                   # scripts/motion-sample.json on ../mock/serve-fixture.json
make motion REDUCE_MOTION=1                   # the same with Reduce Motion on for Prism alone (-reduceMotion YES)
make motion SCRIPT=path/to/script.json        # another scenario
.build/release/prism-motion check build/motion --detail   # every changed frame, its size and place, and late app frames
```

It exits 1 when a step fails. A line reads:

```
 8  track folds   lag 73ms  dur 218ms (asks 200ms)  late 0  dup 0  runs 1  doubles 0  moves 15  ok
```

- **lag**: from the step to the first changed frame. It holds the main-thread work of the change (40 to 60 ms on the
  fixture today) plus the recorder's own delay. Not judged.
- **dur**: first to last changed frame, against what the code asks for (`expect` in the script).
- **late** (fails): frames the app was more than two 60 Hz frames late for while the step moved on screen.
- **dup** (fails): frames inside a moving run that show nothing new: the app drew the same picture twice mid-change.
- **runs** (fails above 1): separate spells of change. A second spell means the state changed twice or two animations
  fought. A spell far smaller than the main one, such as the overlay scroller flashing at the window's edge, is left out.
- **doubles** (fails): frames where part of the changed area already shows the end and part still shows the start, such
  as a card in its old and new place at once. A step with `"crossfade": true` allows it.
- **moves** (fails with Reduce Motion): frames that are not a blend of start and end, meaning something slid or grew
  instead of fading.
- **duration** (fails): a step with `expect` must last no longer than it asks for plus two 60 Hz frames, and at least 40 %
  of it (an ease's last stretch moves less than a picture pixel, so a 0.25 s ease is visible for about 0.1 to 0.2 s). `expect: 0`
  means one frame at most. Under Reduce Motion only the upper bound counts. Frames under 10 % of a step's largest change are a
  tail and neither extend the run nor count as stalls.
- The last line compares the final frame with a still taken after the run (the window controls left out, as a
  screenshot draws them differently).

## Scripts

A script (see `scripts/motion-sample.json`) has `lead` (quiet seconds first; the checker ignores pixels that change on
their own then, such as a working session's activity), `tail` and timed `steps`. Each step has a `name`, `at`
(seconds after the lead) and `do`:

- `event`: `"event": {"op", "kind", "value"}` as `prism server` would send it (a report, a question, a track).
- `pick`: loads `question` into the composer. `answer`: answers `question` with `option` N, or the recommendation.
- `mode`: moves `track` to `mode`. `fold`: `"track:ID"` folds a track's sessions in the sidebar, `"handled"` the
  board's Handled list.
- `wait`: nothing; the step names what happens on its own (the countdown sending).

Optional: `expect` (seconds the animation asks for), `continuous` (changes on its own, such as the countdown: its spells
are not replays), `crossfade`, and `again` (plays the action again that many seconds later in the same step, to see
a change that fires twice fail).

`Prism --fixture F --demo-script S` plays a script without recording. With `--motion-dir D` it waits for the
recorder, then writes `D/timings.json`: each step's start and the display link's frame times, on the host clock that
ScreenCaptureKit stamps its frames with. Each step is also an `OSSignposter` interval (subsystem `dk.thaudal.prism`,
category `motion`) for Instruments.

## Arrivals and leavings (EMI-460)

`make motion-arrive` plays `scripts/motion-arrive.json` on `mock/serve-fixture-long.json` (the fixture with 35 more messages, so the
conversation scrolls): "report arrives", "two reports in one snapshot", "report arrives while scrolled up" (no change at all),
"decision answered on the board" with the card leaving once the countdown sends, "session lands", and an activity-text tick that
changes no frame. Run it with `REDUCE_MOTION=1` too: nothing may slide, the fades stay. A step's two "scroll" lines (`scroll`:
`up` or `bottom`) move the conversation as Emil would. An `events` step applies several changes in one turn. Other agents'
builds load the machine: a few late frames or repeated frames can come from that alone, so rerun a step that fails by one frame.

## The composer (EMI-461)

`make motion-composer` plays `scripts/motion-composer.json` on the fixture: "load D15 into the composer", "load D14 into the composer"
(old and new content cross-fade as the height settles, the board card's ring fades across), "press 1" and "z" (answer, then Undo:
the options fade as the height settles to the countdown row, and back), and "answer D14 and D15 loads" (the countdown sends at
5.0 s and the next decision loads in one height change). The send lands on the deadline itself, not on the 0.2 s tick, and the
card with its ring leaves in the same frame. The ring drains in one linear animation (`ProgressView(timerInterval:)` draws a
spinner on macOS, not a ring); the digit swaps once a second. Run it with `REDUCE_MOTION=1` too: heights jump, fades stay,
the ring still drains.

## How it records, and what the probe found (26 Nov 2026)

`prism-motion record` uses ScreenCaptureKit: an `SCStream` with `SCContentFilter(desktopIndependentWindow:)` for
Prism's window, at most 120 fps, scaled to a quarter of the window's point size and kept as greyscale, each distinct
picture stored once. Nothing is activated, raised or made key: Prism runs with `--background` and `open -g`, as for
`make shots`. It needs the Screen Recording grant the terminal already has for `screencapture`.

A probe window that was never key, never active and ordered behind other windows (occluded):

- The app's display link still ran at 118 fps (median 8.33 ms) on the 120 Hz display, so macOS does not slow the
  drawing of a covered, inactive window down.
- ScreenCaptureKit delivered about 100 fps from it (median 8.8 ms, p95 11.5 ms) and, in Prism runs, 65 to 99 fps with
  gaps up to 200 ms where it delivered nothing at all.

So the film shows order, spells of change, doubles and the end state well, but it skips frames: a gap in the film is
not a stall in the app. Hitches therefore come from the app's own frame times, and a run of change is only split by
frames that show nothing changing, never by a gap in the film. The display link of a background window also slows down
to 40 to 60 Hz when little moves (ProMotion), so an app frame interval only counts as late past two 60 Hz frames
(33 ms). Shorter misses can't be told from the display slowing down.

Reduce Motion: `-reduceMotion YES` on the command line sets `NSWorkspace.accessibilityDisplayShouldReduceMotion`
for Prism alone, without touching the Mac's setting. On 26 Nov the sidebar's fold still slides with it on (`moves`),
so `make motion REDUCE_MOTION=1` fails there until the fold honours it.

## UI states (`make ui-check`, EMI-462)

`make ui-check` plays `scripts/ui-sample.json` on the fixture in light and in dark, in the background, takes the still each
step asks for and compares it with `ui/baselines/<appearance>/<name>.png`. It prints one line per step and exits 1 when
one differs. The baselines are local and not in git (about 23 MB for the sample, light and dark): run `make ui-baseline` on
the build before a change, then `make ui-check` after it, and look at every diff it names. Rerun `make ui-baseline` after a
change that should look different.

```
make ui-check                                 # scripts/ui-sample.json on ../mock/serve-fixture.json
make ui-check UI_SCRIPT=path/to/script.json   # another scenario
make ui-baseline
```

```
light  sidebar-row-hover  (hover a sidebar row)  ok  0.003% changed
dark  board-card-pressed  (press a board card)  FAIL 3.616% changed (156578 px), box 45,279,2560,1029 (x,y,w,h), diff build/ui/dark/board-card-pressed.diff.png, side by side build/ui/dark/board-card-pressed.side.png
```

State is set inside the app: nothing moves the pointer, sends a key or activates Prism. Prism runs with `open -g` and
`--background`; the step asks for a still (`"shot": "name"`), the driving script (`scripts/ui-check.sh`) takes it with
`screencapture -l` by window id and the app waits for it before the next step. Steps, besides the ones above:

- `hover` / `press`: `"target"` is `card:ID` (a board card, or any `RowButtonStyle` target a view names with `.targetID`),
  `row:desk`, `row:track:ID` or `row:session:ID` (sidebar rows). `press` is held until a `press` with no target. Views read
  `WindowModel.hoveredTarget` and `pressedTarget` as they read `foldRequest`; `RowButtonStyle` draws them with its own hover and
  pressed shades. Sidebar rows have no hover of their own yet, so `ScriptedShade` draws the same two shades while a script holds
  the row; when a row gets a real hover (EMI-459) it should read the same state.
- `focus` (`"field"`: `desk`, `answer`, `ask`), `select` (`"item"`: `desk`, `track:ID`, `session:ID`), `dismiss` (Escape on the
  question's card, so the message field shows), `draft` (`"text"`). Existing steps (`pick`, `fold`, `event`, `scroll`) work too.
- `"ui": {"tolerance", "threshold", "shift", "masks"}` at the top of the script: the largest colour difference (0 to 255)
  that is the same pixel, the share of pixels that may differ, how far in pixels a pixel may have moved, and named regions
  (`x`, `y`, `w`, `h` as shares of the window) left out. The sample uses 4, 0.2 %, 1 and none: the sidebar's rows land a
  fraction of a point apart from one launch to the next, so `shift` lets an edge move by a pixel; a hover or pressed shade
  is a whole row or card changing, which it still catches. Hover and pressed differ from each other by 0.8 % (a row) and
  3.6 % (a card) of the window.

A failure writes `build/ui/<appearance>/<name>.diff.png` (the still, changed pixels red, masked regions blue) and
`<name>.side.png` (baseline, still and diff next to each other).

Not covered yet: the Send button's hover and pressed (a system control that draws its own state, with nothing in the app to
set), and scrolling a list to an item.

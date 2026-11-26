# prism tui: screens

Text captures of `prism tui --fixture mock/serve-fixture.json` at 200×50, and the narrower layouts, drawn by
`TestScreensDoc` in `internal/deskui/screens_test.go`; `go test ./internal/deskui -run TestScreensDoc -update`
redraws them. Colours are stripped. Times are UTC and the clock stands at 13:32, the fixture's `generated_at`.
Keys are in docs/keys.md, under `prism tui`.

## Opening

The desk's chat in the middle with the reports as messages, the composer at the bottom, the board on the right.

```text
╭─ Sessions ─────────────────────╮╭─ Prism ────────────────────────────────────────────────────────────────── idle · fixture · serve-fixture.json ─╮╭─ Board ───────────────── 2 need you · 1 running ─╮
│ ▌● Prism                  idle ││                                                                                                                ││ Tuesday 24 Nov                          5 tracks │
│                                ││                                                                                                                ││ 3 landed · 2 ready · 3 in flight                 │
│  FOCUS                         ││                                                                                                                ││ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│  recipe-search  ? Question  2m ││                                                                                                                ││                                                  │
│  bun-rollout          lead · 3 ││                                                                                                                ││ NEEDS YOU                                      2 │
│   ├ pantry-api           ◐ now ││                                                                                                                ││  ◆ D14 recipe-search                   desk · 2m │
│   ├ queue-api           ● done ││                                                                                                                ││    Merge PR #418 in pantry-api now?              │
│   └ app-template-node ○ queued ││                                                                                                                ││    ★1 Merge now  2 Wait for Alex                 │
│  scanner-skips-sh…  ▲ Perm  6m ││                                                                                                                ││    3 Hold until tomorrow                         │
│                                ││                                                                                                                ││    ✓ CI green · Approved · No code freeze        │
│  BACKGROUND                    ││                                                                                                                ││  ▲ D15 scanner-skips-shop-api         agent · 6m │
│  weekly-brief      emil ● done ││                                                                                                                ││    Run gh pr create --draft --base main?         │
│                                ││                                                                                                                ││    ★1 Allow once  2 Allow for this session       │
│  WAITING                       ││                                                                                                                ││    3 Deny                                        │
│  invoice-export      Alex · 3h ││                                                                                                                ││    ✓ A draft PR is where desk sessions stop · T… │
│                                ││                                                                                                                ││                                                  │
│  PARKED 2 ▸                    ││                                                                                                                ││ IN FLIGHT                                      3 │
│                                ││                                                                                                                ││  ◐ pantry-api                  +88 −61 · 9 files │
│                                ││                                                                                                                ││    bun-rollout · Bash: bun test --shard 2/3      │
│                                ││  You  12:57                                                                                                    ││  ○ app-template-node                      queued │
│                                ││    take bun-rollout, one session per repo, stop at PRs                                                         ││    bun-rollout · queued · cap 4                  │
│                                ││                                                                                                                ││  ▲ scanner-skips-shop-api       +31 −4 · 3 files │
│                                ││  Prism  12:58                                                                                                  ││    waiting on permission prompt                  │
│                                ││    Started 3 sessions under bun-rollout. app-template-node waits in the queue until a slot frees.              ││                                                  │
│                                ││    ▸ 4 steps                                                                                                   ││ READY TO LAND                                  2 │
│                                ││                                                                                                                ││  ● recipe-search              +212 −40 · 6 files │
│                                ││  From queue-api  13:21                                                                                         ││    emi-377 · not merged                          │
│                                ││    finished. bun replaces node in the Dockerfile and CI, and bun test passes. Branch bun/queue-api.            ││  ● queue-api                   +14 −14 · 2 files │
│                                ││    +14 −14 · 2 files · bun/queue-api                                                            ready to land  ││    bun-rollout · bun/queue-api · not merged      │
│                                ││                                                                                                                ││                                                  │
│                                ││  From recipe-search  13:26                                                                                     ││ WAITING ON PEOPLE                              1 │
│                                ││    finished. PR #418 is open, CI is green and Jordan approved. Stopped before merging.                         ││  invoice-export                        Alex · 3h │
│                                ││    +212 −40 · 6 files · emi-377                                                                 ready to land  ││                                                  │
│                                ││                                                                                                                ││  LANDED                                      3 ▸ │
│                                ││  From scanner-skips-shop-api  13:26                                                                            ││                                                  │
│                                ││    is waiting on a permission prompt:                                                                          ││                                                  │
│                                ││    gh pr create --draft --base main                                                                            ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││  · Prism re-asked these as decisions                                                                     13:26 ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││  Prism  13:27                                                                                                  ││                                                  │
│                                ││    Two need you: D14 and D15. I open the queue-api PR next unless you say no.                                  ││                                                  │
│                                ││    ▸ 2 steps                                                                                                   ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││ ╭─ Message Prism ────────────────────────────────────────────────────────────────────────────────────────────╮ ││                                                  │
│                                ││ │ ▌Write to Prism…                                                                                           │ ││                                                  │
│                                ││ ╰─ 2 questions on the board · enter send ────────────────────────────────────────────────────────────────────╯ ││                                                  │
╰────────────────────────────────╯╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯╰──────────────────────────────────────────────────╯
 enter send  alt+enter new line  ↑↓ scroll  ctrl+u clear  tab column                                                                                                      ● fixture · serve-fixture.json
```

## A question selected

tab to the board: the cursor is on D14, its evidence and link unfold, and the composer becomes D14's.

```text
╭─ Sessions ─────────────────────╮╭─ Prism ────────────────────────────────────────────────────────────────── idle · fixture · serve-fixture.json ─╮╭─ Board ───────────────── 2 need you · 1 running ─╮
│ ▌● Prism                  idle ││                                                                                                                ││ Tuesday 24 Nov                          5 tracks │
│                                ││                                                                                                                ││ 3 landed · 2 ready · 3 in flight                 │
│  FOCUS                         ││                                                                                                                ││ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│  recipe-search  ? Question  2m ││                                                                                                                ││                                                  │
│  bun-rollout          lead · 3 ││                                                                                                                ││ NEEDS YOU                                      2 │
│   ├ pantry-api           ◐ now ││                                                                                                                ││ ▌◆ D14 recipe-search                   desk · 2m │
│   ├ queue-api           ● done ││                                                                                                                ││    Merge PR #418 in pantry-api now?              │
│   └ app-template-node ○ queued ││                                                                                                                ││    ★1 Merge now  2 Wait for Alex                 │
│  scanner-skips-sh…  ▲ Perm  6m ││                                                                                                                ││    3 Hold until tomorrow                         │
│                                ││  You  12:57                                                                                                    ││    ✓ CI green                    CircleCI #48211 │
│  BACKGROUND                    ││    take bun-rollout, one session per repo, stop at PRs                                                         ││    ✓ Approved                      Jordan, 12:51 │
│  weekly-brief      emil ● done ││                                                                                                                ││    ✓ No code freeze                   Gatekeeper │
│                                ││  Prism  12:58                                                                                                  ││    o https://github.com/acme/pantry-api/pull/418 │
│  WAITING                       ││    Started 3 sessions under bun-rollout. app-template-node waits in the queue until a slot frees.              ││  ▲ D15 scanner-skips-shop-api         agent · 6m │
│  invoice-export      Alex · 3h ││    ▸ 4 steps                                                                                                   ││    Run gh pr create --draft --base main?         │
│                                ││                                                                                                                ││    ★1 Allow once  2 Allow for this session       │
│  PARKED 2 ▸                    ││  From queue-api  13:21                                                                                         ││    3 Deny                                        │
│                                ││    finished. bun replaces node in the Dockerfile and CI, and bun test passes. Branch bun/queue-api.            ││    ✓ A draft PR is where desk sessions stop · T… │
│                                ││    +14 −14 · 2 files · bun/queue-api                                                            ready to land  ││                                                  │
│                                ││                                                                                                                ││ IN FLIGHT                                      3 │
│                                ││  From recipe-search  13:26                                                                                     ││  ◐ pantry-api                  +88 −61 · 9 files │
│                                ││    finished. PR #418 is open, CI is green and Jordan approved. Stopped before merging.                         ││    bun-rollout · Bash: bun test --shard 2/3      │
│                                ││    +212 −40 · 6 files · emi-377                                                                 ready to land  ││  ○ app-template-node                      queued │
│                                ││                                                                                                                ││    bun-rollout · queued · cap 4                  │
│                                ││  From scanner-skips-shop-api  13:26                                                                            ││  ▲ scanner-skips-shop-api       +31 −4 · 3 files │
│                                ││    is waiting on a permission prompt:                                                                          ││    waiting on permission prompt                  │
│                                ││    gh pr create --draft --base main                                                                            ││                                                  │
│                                ││                                                                                                                ││ READY TO LAND                                  2 │
│                                ││  · Prism re-asked these as decisions                                                                     13:26 ││  ● recipe-search              +212 −40 · 6 files │
│                                ││                                                                                                                ││    emi-377 · not merged                          │
│                                ││  Prism  13:27                                                                                                  ││  ● queue-api                   +14 −14 · 2 files │
│                                ││    Two need you: D14 and D15. I open the queue-api PR next unless you say no.                                  ││    bun-rollout · bun/queue-api · not merged      │
│                                ││    ▸ 2 steps                                                                                                   ││                                                  │
│                                ││                                                                                                                ││ WAITING ON PEOPLE                              1 │
│                                ││ ╭─ D14 recipe-search · Merge PR #418 in pantry-api now? ─────────────────────────────────────────────────────╮ ││  invoice-export                        Alex · 3h │
│                                ││ │ ^1 Merge now, squash                                                                           recommended │ ││                                                  │
│                                ││ │    CI green · Jordan approved · +212 −40 in 6 files                                                        │ ││  LANDED                                      3 ▸ │
│                                ││ │ ^2 Wait for Alex                                                                                           │ ││                                                  │
│                                ││ │    He owns the refund rules. Back at 15:00.                                                                │ ││                                                  │
│                                ││ │ ^3 Hold until tomorrow                                                                                     │ ││                                                  │
│                                ││ │    Ships with the 09:00 release train.                                                                     │ ││                                                  │
│                                ││ │                                                                                                            │ ││                                                  │
│                                ││ │ Or answer in your own words… · r on the board                                                              │ ││                                                  │
│                                ││ ╰─ 1 of 2 · enter recommended · esc Prism ───────────────────────────────────────────────────────────────────╯ ││                                                  │
│                                ││ ╭─ Message Prism ────────────────────────────────────────────────────────────────────────────────────────────╮ ││                                                  │
│                                ││ │ Write to Prism…                                                                                            │ ││                                                  │
│                                ││ ╰─ 2 questions on the board · enter send ────────────────────────────────────────────────────────────────────╯ ││                                                  │
╰────────────────────────────────╯╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯╰──────────────────────────────────────────────────╯
 enter recommended  1-9 pick  r in words  z undo  t take over  g give back  o link  tab column  ? keys                                                                    ● fixture · serve-fixture.json
```

## Answer, then the countdown

enter takes the recommendation; it is sent when the 5 s run out, z takes it back. Two seconds in:

```text
╭─ Sessions ─────────────────────╮╭─ Prism ────────────────────────────────────────────────────────────────── idle · fixture · serve-fixture.json ─╮╭─ Board ───────────────── 2 need you · 1 running ─╮
│ ▌● Prism                  idle ││                                                                                                                ││ Tuesday 24 Nov                          5 tracks │
│                                ││                                                                                                                ││ 3 landed · 2 ready · 3 in flight                 │
│  FOCUS                         ││                                                                                                                ││ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│  recipe-search  ? Question  2m ││                                                                                                                ││                                                  │
│  bun-rollout          lead · 3 ││                                                                                                                ││ NEEDS YOU                                      2 │
│   ├ pantry-api           ◐ now ││                                                                                                                ││ ▌◆ D14 recipe-search                   desk · 2m │
│   ├ queue-api           ● done ││                                                                                                                ││    Merge PR #418 in pantry-api now?              │
│   └ app-template-node ○ queued ││                                                                                                                ││    → Merge now, squash      sending in 3 s · z   │
│  scanner-skips-sh…  ▲ Perm  6m ││                                                                                                                ││  ▲ D15 scanner-skips-shop-api         agent · 6m │
│                                ││  You  12:57                                                                                                    ││    Run gh pr create --draft --base main?         │
│  BACKGROUND                    ││    take bun-rollout, one session per repo, stop at PRs                                                         ││    ★1 Allow once  2 Allow for this session       │
│  weekly-brief      emil ● done ││                                                                                                                ││    3 Deny                                        │
│                                ││  Prism  12:58                                                                                                  ││    ✓ A draft PR is where desk sessions stop · T… │
│  WAITING                       ││    Started 3 sessions under bun-rollout. app-template-node waits in the queue until a slot frees.              ││                                                  │
│  invoice-export      Alex · 3h ││    ▸ 4 steps                                                                                                   ││ IN FLIGHT                                      3 │
│                                ││                                                                                                                ││  ◐ pantry-api                  +88 −61 · 9 files │
│  PARKED 2 ▸                    ││  From queue-api  13:21                                                                                         ││    bun-rollout · Bash: bun test --shard 2/3      │
│                                ││    finished. bun replaces node in the Dockerfile and CI, and bun test passes. Branch bun/queue-api.            ││  ○ app-template-node                      queued │
│                                ││    +14 −14 · 2 files · bun/queue-api                                                            ready to land  ││    bun-rollout · queued · cap 4                  │
│                                ││                                                                                                                ││  ▲ scanner-skips-shop-api       +31 −4 · 3 files │
│                                ││  From recipe-search  13:26                                                                                     ││    waiting on permission prompt                  │
│                                ││    finished. PR #418 is open, CI is green and Jordan approved. Stopped before merging.                         ││                                                  │
│                                ││    +212 −40 · 6 files · emi-377                                                                 ready to land  ││ READY TO LAND                                  2 │
│                                ││                                                                                                                ││  ● recipe-search              +212 −40 · 6 files │
│                                ││  From scanner-skips-shop-api  13:26                                                                            ││    emi-377 · not merged                          │
│                                ││    is waiting on a permission prompt:                                                                          ││  ● queue-api                   +14 −14 · 2 files │
│                                ││    gh pr create --draft --base main                                                                            ││    bun-rollout · bun/queue-api · not merged      │
│                                ││                                                                                                                ││                                                  │
│                                ││  · Prism re-asked these as decisions                                                                     13:26 ││ WAITING ON PEOPLE                              1 │
│                                ││                                                                                                                ││  invoice-export                        Alex · 3h │
│                                ││  Prism  13:27                                                                                                  ││                                                  │
│                                ││    Two need you: D14 and D15. I open the queue-api PR next unless you say no.                                  ││  LANDED                                      3 ▸ │
│                                ││    ▸ 2 steps                                                                                                   ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││ ╭─ Merge now, squash · sending in 3 s · z undo ──────────────────────────────────────────────────────────────╮ ││                                                  │
│                                ││ │ ^1 Merge now, squash                                                                               sending │ ││                                                  │
│                                ││ │    CI green · Jordan approved · +212 −40 in 6 files                                                        │ ││                                                  │
│                                ││ │ ^2 Wait for Alex                                                                                           │ ││                                                  │
│                                ││ │    He owns the refund rules. Back at 15:00.                                                                │ ││                                                  │
│                                ││ │ ^3 Hold until tomorrow                                                                                     │ ││                                                  │
│                                ││ │    Ships with the 09:00 release train.                                                                     │ ││                                                  │
│                                ││ │                                                                                                            │ ││                                                  │
│                                ││ │ Or answer in your own words… · r on the board                                                              │ ││                                                  │
│                                ││ ╰─ D14 · z takes it back ────────────────────────────────────────────────────────────────────────────────────╯ ││                                                  │
│                                ││ ╭─ Message Prism ────────────────────────────────────────────────────────────────────────────────────────────╮ ││                                                  │
│                                ││ │ Write to Prism…                                                                                            │ ││                                                  │
│                                ││ ╰─ 2 questions on the board · enter send ────────────────────────────────────────────────────────────────────╯ ││                                                  │
╰────────────────────────────────╯╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯╰──────────────────────────────────────────────────╯
 enter recommended  1-9 pick  r in words  z undo  t take over  g give back  o link  tab column  ? keys                                                                    ● fixture · serve-fixture.json
```

## Typing while the desk talks

Emil types; two desk entries arrive. The composer and its text stay put, and the entries wait under "2 new".

```text
╭─ Sessions ─────────────────────╮╭─ Prism ────────────────────────────────────────────────────────────────── idle · fixture · serve-fixture.json ─╮╭─ Board ───────────────── 2 need you · 1 running ─╮
│ ▌● Prism                  idle ││                                                                                                                ││ Tuesday 24 Nov                          5 tracks │
│                                ││                                                                                                                ││ 3 landed · 2 ready · 3 in flight                 │
│  FOCUS                         ││                                                                                                                ││ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│  recipe-search  ? Question  2m ││                                                                                                                ││                                                  │
│  bun-rollout          lead · 3 ││                                                                                                                ││ NEEDS YOU                                      2 │
│   ├ pantry-api           ◐ now ││                                                                                                                ││  ◆ D14 recipe-search                   desk · 2m │
│   ├ queue-api           ● done ││                                                                                                                ││    Merge PR #418 in pantry-api now?              │
│   └ app-template-node ○ queued ││                                                                                                                ││    ★1 Merge now  2 Wait for Alex                 │
│  scanner-skips-sh…  ▲ Perm  6m ││                                                                                                                ││    3 Hold until tomorrow                         │
│                                ││                                                                                                                ││    ✓ CI green · Approved · No code freeze        │
│  BACKGROUND                    ││                                                                                                                ││  ▲ D15 scanner-skips-shop-api         agent · 6m │
│  weekly-brief      emil ● done ││                                                                                                                ││    Run gh pr create --draft --base main?         │
│                                ││                                                                                                                ││    ★1 Allow once  2 Allow for this session       │
│  WAITING                       ││                                                                                                                ││    3 Deny                                        │
│  invoice-export      Alex · 3h ││                                                                                                                ││    ✓ A draft PR is where desk sessions stop · T… │
│                                ││                                                                                                                ││                                                  │
│  PARKED 2 ▸                    ││                                                                                                                ││ IN FLIGHT                                      3 │
│                                ││                                                                                                                ││  ◐ pantry-api                  +88 −61 · 9 files │
│                                ││                                                                                                                ││    bun-rollout · Bash: bun test --shard 2/3      │
│                                ││  You  12:57                                                                                                    ││  ○ app-template-node                      queued │
│                                ││    take bun-rollout, one session per repo, stop at PRs                                                         ││    bun-rollout · queued · cap 4                  │
│                                ││                                                                                                                ││  ▲ scanner-skips-shop-api       +31 −4 · 3 files │
│                                ││  Prism  12:58                                                                                                  ││    waiting on permission prompt                  │
│                                ││    Started 3 sessions under bun-rollout. app-template-node waits in the queue until a slot frees.              ││                                                  │
│                                ││    ▸ 4 steps                                                                                                   ││ READY TO LAND                                  2 │
│                                ││                                                                                                                ││  ● recipe-search              +212 −40 · 6 files │
│                                ││  From queue-api  13:21                                                                                         ││    emi-377 · not merged                          │
│                                ││    finished. bun replaces node in the Dockerfile and CI, and bun test passes. Branch bun/queue-api.            ││  ● queue-api                   +14 −14 · 2 files │
│                                ││    +14 −14 · 2 files · bun/queue-api                                                            ready to land  ││    bun-rollout · bun/queue-api · not merged      │
│                                ││                                                                                                                ││                                                  │
│                                ││  From recipe-search  13:26                                                                                     ││ WAITING ON PEOPLE                              1 │
│                                ││    finished. PR #418 is open, CI is green and Jordan approved. Stopped before merging.                         ││  invoice-export                        Alex · 3h │
│                                ││    +212 −40 · 6 files · emi-377                                                                 ready to land  ││                                                  │
│                                ││                                                                                                                ││  LANDED                                      3 ▸ │
│                                ││  From scanner-skips-shop-api  13:26                                                                            ││                                                  │
│                                ││    is waiting on a permission prompt:                                                                          ││                                                  │
│                                ││    gh pr create --draft --base main                                                                            ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││  · Prism re-asked these as decisions                                                                     13:26 ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││  Prism  13:27                                                                                                  ││                                                  │
│                                ││    Two need you: D14 and D15. I open the queue-api PR next unless you say no.                                  ││                                                  │
│                                ││    ▸ 2 steps                                                                                                   ││                                                  │
│                                ││                                                                                            ▾ 2 new · ↓ to read ││                                                  │
│                                ││ ╭─ Message Prism ────────────────────────────────────────────────────────────────────────────────────────────╮ ││                                                  │
│                                ││ │ merge 418, and hold the queue-api PR until I have looked▌                                                  │ ││                                                  │
│                                ││ ╰─ 2 questions on the board · enter send ────────────────────────────────────────────────────────────────────╯ ││                                                  │
╰────────────────────────────────╯╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯╰──────────────────────────────────────────────────╯
 enter send  alt+enter new line  ↑↓ scroll  ctrl+u clear  tab column                                                                                                      ● fixture · serve-fixture.json
```

## A background session, read-only

In the sessions column, pantry-api: its transcript with tool calls folded into steps. t takes it over, g gives it back.

```text
╭─ Sessions ─────────────────────╮╭─ pantry-api · transcript ────────────────────────────────────────────────────────── desk · background · ◐ now ─╮╭─ Board ───────────────── 2 need you · 1 running ─╮
│  ● Prism                  idle ││                                                                                                                ││ Tuesday 24 Nov                          5 tracks │
│                                ││  Now: Bash: bun test --shard 2/3                                                                               ││ 3 landed · 2 ready · 3 in flight                 │
│  FOCUS                         ││                                                                                                                ││ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│  recipe-search  ? Question  2m ││  Prism  12:58                                                                                                  ││                                                  │
│  bun-rollout          lead · 3 ││    Replace node with bun in pantry-api: Dockerfile, CI and package scripts. Run bun test. Stop at a commit on  ││ NEEDS YOU                                      2 │
│ ▌ ├ pantry-api           ◐ now ││    bun/pantry-api.                                                                                             ││  ◆ D14 recipe-search                   desk · 2m │
│   ├ queue-api           ● done ││                                                                                                                ││    Merge PR #418 in pantry-api now?              │
│   └ app-template-node ○ queued ││  pantry-api  13:20                                                                                             ││    ★1 Merge now  2 Wait for Alex                 │
│  scanner-skips-sh…  ▲ Perm  6m ││    Swapped the base image to oven/bun:1.3 and moved the scripts to bun run. Running the test suite in three    ││    3 Hold until tomorrow                         │
│                                ││    shards.                                                                                                     ││    ✓ CI green                    CircleCI #48211 │
│  BACKGROUND                    ││    ▸ 12 steps                                                                                                  ││    ✓ Approved                      Jordan, 12:51 │
│  weekly-brief      emil ● done ││                                                                                                                ││    ✓ No code freeze                   Gatekeeper │
│                                ││  pantry-api  13:31                                                                                             ││    o https://github.com/acme/pantry-api/pull/418 │
│  WAITING                       ││    Shard 1/3 passed. Shard 2/3 is running.                                                                     ││  ▲ D15 scanner-skips-shop-api         agent · 6m │
│  invoice-export      Alex · 3h ││    ▸ 3 steps                                                                                                   ││    Run gh pr create --draft --base main?         │
│                                ││                                                                                                                ││    ★1 Allow once  2 Allow for this session       │
│  PARKED 2 ▸                    ││  pantry-api  13:33                                                                                             ││    3 Deny                                        │
│                                ││    Here is the shard summary.                                                                                  ││    ✓ A draft PR is where desk sessions stop · T… │
│                                ││                                                                                                                ││                                                  │
│                                ││    ## Shard results                                                                                            ││ IN FLIGHT                                      3 │
│                                ││                                                                                                                ││  ◐ pantry-api                  +88 −61 · 9 files │
│                                ││    ```go                                                                                                       ││    bun-rollout · Bash: bun test --shard 2/3      │
│                                ││    func shard(i, n int, tests []string) []string {                                                             ││  ○ app-template-node                      queued │
│                                ││     var out []string                                                                                           ││    bun-rollout · queued · cap 4                  │
│                                ││     for j := i; j < len(tests); j += n {                                                                       ││  ▲ scanner-skips-shop-api       +31 −4 · 3 files │
│                                ││      out = append(out, tests[j])                                                                               ││    waiting on permission prompt                  │
│                                ││     }                                                                                                          ││                                                  │
│                                ││     return out                                                                                                 ││ READY TO LAND                                  2 │
│                                ││    }                                                                                                           ││  ● recipe-search              +212 −40 · 6 files │
│                                ││    ```                                                                                                         ││    emi-377 · not merged                          │
│                                ││                                                                                                                ││  ● queue-api                   +14 −14 · 2 files │
│                                ││    | Shard | Tests | Result |                                                                                  ││    bun-rollout · bun/queue-api · not merged      │
│                                ││    |:--|--:|:--|                                                                                               ││                                                  │
│                                ││    | 1/3 | 42 | passed |                                                                                       ││ WAITING ON PEOPLE                              1 │
│                                ││    | 2/3 | 40 | running |                                                                                      ││  invoice-export                        Alex · 3h │
│                                ││    | 3/3 | 41 | queued, waiting for a free runner before it starts |                                           ││                                                  │
│                                ││                                                                                                                ││  LANDED                                      3 ▸ │
│                                ││    - Dockerfile                                                                                                ││                                                  │
│                                ││      - base image is oven/bun:1.3                                                                              ││                                                  │
│                                ││      - lockfile regenerated                                                                                    ││                                                  │
│                                ││    - CI                                                                                                        ││                                                  │
│                                ││    - Package scripts                                                                                           ││                                                  │
│                                ││    ▸ 5 steps                                                                                                   ││                                                  │
│                                ││                                                                                                                ││                                                  │
│                                ││ ╭─ read-only ────────────────────────────────────────────────────────────────────────────────────────────────╮ ││                                                  │
│                                ││ │ Read-only. t takes it over in Rex, g gives it back, esc returns to Prism.                                  │ ││                                                  │
│                                ││ ╰────────────────────────────────────────────────────────────────────────────────────────────────────────────╯ ││                                                  │
╰────────────────────────────────╯╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯╰──────────────────────────────────────────────────╯
 ↑↓ move  enter open  space fold  t take over  g give back  o link  tab column  ? keys  q quit                                                                            ● fixture · serve-fixture.json
```

## Two columns (120×40)

Under 150 columns the sessions fold away: the board stands beside the chat, and the sessions take its place while they have focus.

```text
╭─ Prism ────────────────────────── idle · fixture · serve-fixture.json ─╮╭─ Board ─────────── 2 need you · 1 running ─╮
│                                                                        ││ Tuesday 24 Nov                    5 tracks │
│                                                                        ││ 3 landed · 2 ready · 3 in flight           │
│                                                                        ││ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│                                                                        ││                                            │
│                                                                        ││ NEEDS YOU                                2 │
│  You  12:57                                                            ││  ◆ D14 recipe-search             desk · 2m │
│    take bun-rollout, one session per repo, stop at PRs                 ││    Merge PR #418 in pantry-api now?        │
│                                                                        ││    ★1 Merge now  2 Wait for Alex           │
│  Prism  12:58                                                          ││    3 Hold until tomorrow                   │
│    Started 3 sessions under bun-rollout. app-template-node waits in    ││    ✓ CI green · Approved · No code freeze  │
│    the queue until a slot frees.                                       ││  ▲ D15 scanner-skips-shop-api   agent · 6m │
│    ▸ 4 steps                                                           ││    Run gh pr create --draft --base main?   │
│                                                                        ││    ★1 Allow once  2 Allow for this session │
│  From queue-api  13:21                                                 ││    3 Deny                                  │
│    finished. bun replaces node in the Dockerfile and CI, and bun test  ││    ✓ A draft PR is where desk sessions st… │
│    passes. Branch bun/queue-api.                                       ││                                            │
│    +14 −14 · 2 files · bun/queue-api                    ready to land  ││ IN FLIGHT                                3 │
│                                                                        ││  ◐ pantry-api            +88 −61 · 9 files │
│  From recipe-search  13:26                                             ││    bun-rollout · Bash: bun test --shard 2… │
│    finished. PR #418 is open, CI is green and Jordan approved.         ││  ○ app-template-node                queued │
│    Stopped before merging.                                             ││    bun-rollout · queued · cap 4            │
│    +212 −40 · 6 files · emi-377                         ready to land  ││  ▲ scanner-skips-shop-api +31 −4 · 3 files │
│                                                                        ││    waiting on permission prompt            │
│  From scanner-skips-shop-api  13:26                                    ││                                            │
│    is waiting on a permission prompt:                                  ││ READY TO LAND                            2 │
│    gh pr create --draft --base main                                    ││  ● recipe-search        +212 −40 · 6 files │
│                                                                        ││    emi-377 · not merged                    │
│  · Prism re-asked these as decisions                             13:26 ││  ● queue-api             +14 −14 · 2 files │
│                                                                        ││    bun-rollout · bun/queue-api · not merg… │
│  Prism  13:27                                                          ││                                            │
│    Two need you: D14 and D15. I open the queue-api PR next unless you  ││ WAITING ON PEOPLE                        1 │
│    say no.                                                             ││  invoice-export                  Alex · 3h │
│    ▸ 2 steps                                                           ││                                            │
│                                                                        ││  LANDED                                3 ▸ │
│ ╭─ Message Prism ────────────────────────────────────────────────────╮ ││                                            │
│ │ ▌Write to Prism…                                                   │ ││                                            │
│ ╰─ 2 questions on the board · enter send ────────────────────────────╯ ││                                            │
╰────────────────────────────────────────────────────────────────────────╯╰────────────────────────────────────────────╯
 enter send  alt+enter new line  ↑↓ scroll  ctrl+u clear  tab column                      ● fixture · serve-fixture.json
```

## One column (90×30)

Under 100 columns only the focused column shows; its title bar names where tab goes.

```text
╭─ Board ──────────────────────────────── 2 need you · 1 running · tab Sessions · Prism ─╮
│ Tuesday 24 Nov                                                                5 tracks │
│ 3 landed · 2 ready · 3 in flight                                                       │
│ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│                                                                                        │
│ NEEDS YOU                                                                            2 │
│ ▌◆ D14 recipe-search                                                         desk · 2m │
│    Merge PR #418 in pantry-api now?                                                    │
│    ★1 Merge now  2 Wait for Alex  3 Hold until tomorrow                                │
│    ✓ CI green                                                          CircleCI #48211 │
│    ✓ Approved                                                            Jordan, 12:51 │
│    ✓ No code freeze                                                         Gatekeeper │
│    o https://github.com/acme/pantry-api/pull/418                                       │
│  ▲ D15 scanner-skips-shop-api                                               agent · 6m │
│    Run gh pr create --draft --base main?                                               │
│    ★1 Allow once  2 Allow for this session  3 Deny                                     │
│    ✓ A draft PR is where desk sessions stop · Tier 2 track                             │
│                                                                                        │
│ IN FLIGHT                                                                            3 │
│  ◐ pantry-api                                                        +88 −61 · 9 files │
│    bun-rollout · Bash: bun test --shard 2/3                                            │
│  ○ app-template-node                                                            queued │
│    bun-rollout · queued · cap 4                                                        │
│  ▲ scanner-skips-shop-api                                             +31 −4 · 3 files │
│    waiting on permission prompt                                                        │
│                                                                                        │
│ READY TO LAND                                                                        2 │
│  ● recipe-search                                                    +212 −40 · 6 files │
╰────────────────────────────────────────────────────────────────────────────────────────╯
 enter recommended  1-9 pick  r in words  z undo  t take o… ● fixture · serve-fixture.json
```

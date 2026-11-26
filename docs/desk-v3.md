# Desk v3: the desk owns what it starts (2026-11-24)

Status: draft for Emil's decision. No code yet. Building it is tier 3 (the desk sends text to agents); this doc is tier 1.

## Why v2 is still heavy

Desk v2 made the desk a conversation, and that part works. What keeps growing is the plumbing around it. Most commits since EMI-363 handle one problem: the desk supervises sessions it does not own. Emil starts tracks and types into them in Rex, and the desk watches the same sessions from outside. So it has to guess what state they are in:

- it reads screens, and took Claude Code's Tab suggestion for Emil's answer (memory: desk-prompt-suggestions);
- it refuses to type into a busy agent, and retries on a later event;
- `tracks ask --wait` has to find the Stop for the question it typed, past compactions and interrupts (EMI-371, three review rounds);
- a `[desk]` mark, a take-back with `u` and its locks keep the desk's answers apart from Emil's (EMI-363, EMI-369);
- word lists decide which asks are decisions ("rolling back", "terraform apply"), with NFKC folding (EMI-363, EMI-369).

Each fix is right on its own. Together they are the cost of watching peers instead of owning children.

## What Superego's chief of staff does

Kim's chief-of-staff profile for Superego (github.com/example/agent-profiles, `profiles/chief-of-staff`):

- **The lead owns its lanes.** Every worker is a child thread it started. When a child finishes, fails or asks a question, the runtime sends the parent a message, and that message starts the parent's turn. Nothing polls and nothing reads a screen.
- **Typing into a lane hands it over.** Once the user writes to a child, that conversation is the user's and stops reporting to the lead.
- **Follow-ups wait for idle.** `lane_send` to a busy lane holds one note, merged with any earlier one, and delivers it when the lane goes idle. It never stacks messages and never types into a busy agent.
- **Structure, not wording, enforces the rules.** Workers have no thread tools, so they cannot message each other. The prompt stays at about 60 lines.
- **A cap of 4 running lanes**; more starts queue.
- **Decisions don't block.** `board_ask` puts a decision on a board, and the lead keeps working on everything that does not depend on it.
- **Across projects, a list.** The sidebar collects what waits on the user from every lead. No agent sits above them.

## Primitives (checked against Superego, 2026-11-24)

Bad primitives make everything after them harder, so this comes before any v3 code. The store today (`internal/store/schema.go`, schema 7) has `tracks`, `names`, `checks`, `asks`, `blockers`, `events`, `desk`, `desk_asks` and `desk_log`. Superego's whole model is machine, project, thread and pane. A thread is one conversation, with a parent, a folder, a worktree and a status the runtime derives. Questions, reports and notes are records about threads.

### What is wrong today

1. **A track is three things in one row.** It is an intent (goal, mode, rank, today, waiting on, check), an agent session (`rex_session`, `claude_session`, `claude_cwd`) and a status page the agent writes about itself (`where`, asks). So a track can't have no agent (waiting on Alex), can't have two (a lead and its threads have to be tracks of their own) and can't change agent without rebinding. Lead mode had to make threads into tracks (`extra.meta.parent`, `role: lead`), and the desk had to be a special table, because the model has no word for "a session".
2. **Status is guessed from outside.** Working, idle and blocked come from Rex and from reading screens, so every client and the desk infer them again. Superego derives one status vocabulary (`idle | working | done | blocked{permission|question} | error`) in the runtime and stores "seen" per thread, so "done" means "finished and you haven't looked".
3. **Three kinds of question.** `asks` (free text lines on a card), `desk_asks` (with a recommendation and evidence) and check asks (matched by text) are the same thing three times, and none of them has options.
4. **No messages.** The desk, a lead and Emil all reach an agent by typing into its pane with a text prefix (`[desk]`, `[lead X]`). Whether a prompt counts as Emil's comes from matching that prefix, and a busy agent means refusing and trying again. Superego makes a message a record (`From [thread]: …`) that the runtime delivers when the thread is free.
5. **Two logs.** `events` (the attention log) and `desk_log` both record who did what.

### v3 primitives

| Primitive | What it is | Replaces |
|---|---|---|
| **Track** | Emil's intent: goal, mode, tier, rank, today, blocked by, waiting on, check, links, where, later. It can have no session (waiting on a person) or several over time. | the session columns on `tracks`; `role`, `parent` in `extra` |
| **Session** | One Claude Code conversation. Id = Claude's session id. `track` (none for the desk), `parent` (the desk or a lead session), `owner` (desk or emil), `host` (background, rex or stopped), cwd, worktree and branch, model, `status`, `seen_at`, last answer. A lead is just a session with children; the desk is a session with no track. | `desk` table, lead role, threads as tracks |
| **Question** | Something Emil (or a parent) has to decide. Track and session, `from` (agent, desk or check), the question, up to 9 options (label and one-line description), the recommended one, evidence lines with sources, links, and the answer (option or words, by whom, when). | `asks`, `desk_asks`, check asks |
| **Message** | Text for a session: `from` (emil, desk, a session or the system), `kind` (note, answer or report), when it was delivered. A note to a busy session is held and merged, and delivered at its next Stop. A child's report to its parent is a message too. | typed `[desk]`/`[lead]` prefixes, busy refusals, `tracks ask --wait` |
| **Event** | The append-only log: who (emil, desk, a session, the system) did what to which track or session. The pane's handled log is a view of it. | `events` + `desk_log` |
| Check, Name, Blocker | Unchanged. A check that flips makes a Question. | |

**Status comes from hooks, not screens.** SessionStart, UserPromptSubmit, Stop and Notification (permission) write a session's status. `claude agents --json` serves as the liveness check: it already lists every interactive and background session with its state. Nothing reads a Rex screen.

**Ownership falls out of the model.** A session's `owner` is `desk` when the desk started it. UserPromptSubmit sets it to `emil` when a prompt arrives that no Message delivered.

## The window

Emil wants v3 used only through one window, laid out like Superego's chief of staff (screenshot, 2026-11-24): a full-screen Rex session with threads on the left, the conversation in the middle and the board on the right. Sessions the desk starts run in the background with no Rex pane at all. Emil sees one only when he asks to take it over, so giving up micromanagement is the default and looking is a deliberate act.

**Background sessions are built into Claude Code** (checked 2026-11-24 on this machine): `claude --bg` starts a session with no terminal and prints its id, `claude agents --json` lists sessions with their state (one pan-88 track already runs that way, `blocked`), `claude attach ID` opens one in a terminal, and `claude stop`, `logs` and `respawn` manage them. That gives desk sessions the host they need without tracks running its own agent process.

Layout:

- **Left, sessions.** The desk on top. Then tracks by mode, each with its sessions nested under it, and each row with a status chip: Question, a spinner while working, a dot when done and unseen, Error. Tracks with no session show what they wait on.
- **Middle, the conversation.** The desk's chat. Selecting a background session shows its transcript read-only (from Claude's JSONL; tool calls folded to "7 steps", as Superego does) and its reports. `t` takes it over: `claude attach` in a new Rex pane, and the session's owner becomes Emil.
- **Right, the board.** Needs you: questions with numbered options and the recommendation marked, answered with `enter`, `1`-`9` or `r`, and `z` within 5 seconds (see Answering). In flight: running sessions with what they're doing now and their diff stats. Ready to land: finished, with branch and changes. Landed: folded.

Two ways to build it:

- **A, first.** A Rex session with two panes. On the left is the desk as an ordinary interactive Claude Code session. On the right is the tracks TUI, with the sessions list over the board and `enter` swapping the board for a session's transcript. Desk sessions run as `--bg`. It looks like the screenshot within a few days of work and tests the ownership model and the board.
- **B, when A feels right.** One TUI with all three columns, the desk itself running headless behind `tracks serve` (stream-json), and the same daemon as the API for a native app (see Headless).

### Step 0 results (probed 2026-11-24, Claude Code on this machine, Haiku sessions in a scratch folder)

- **Hooks run in `--bg` sessions.** SessionStart, UserPromptSubmit, Stop and PermissionRequest all fired, with the session id, cwd and transcript path. **Stop carries `last_assistant_message`**, so a report to the parent needs no transcript parsing.
- **A background session's default permission mode is `auto`**, as for interactive sessions.
- **`--bg` refuses an untrusted folder** at once ("Workspace not trusted…", exit 1) instead of hanging on a dialog. That makes `tracks spawn`'s 10-second dialog watch unnecessary.
- **A finished background session stays alive and idle** (`state: done`, `status: idle`, with a pid). `--resume` on it, or on a stopped one with any flags, starts a copy with a new id.
- **Delivering a message works:** `claude stop ID`, then `claude --bg --resume SESSION_ID "text"` *with no other flags*. Claude wakes the same session with its saved options (name, settings, model) and the text as the next prompt. About 4 seconds end to end. So a held note is delivered at Stop by stop + resume, and never typed into a pane.
- **Permission prompts can be answered from outside.** The session shows `state: blocked`, `waitingFor: "permission prompt"`, and a PermissionRequest hook gets `tool_name` and `tool_input`. A hook that waits (here 10 s, timeout set to 300 s) and then prints `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` let the session go on. So a permission prompt can become a Question on the board. The hook should give up after a few minutes and leave the prompt to `claude attach`.
- **`claude agents --json` is the liveness source:** `kind` (background or interactive), `state` (done, blocked), `status` (idle, busy, waiting), `waitingFor`, the pid, and the session id.
- **A prompt's hook payload doesn't say where the prompt came from.** A resumed note and a typed prompt look alike. The hand-back therefore matches UserPromptSubmit's `prompt` against the Messages tracks delivered: any other prompt is Emil's.
- **Take-over works in a real terminal.** Emil ran `claude attach ID` in a Rex pane and typed a prompt. It fired UserPromptSubmit and Stop in the same process (same pid, no restart), and after `Ctrl+Z` the session stayed in the background, idle. `attach` shows nothing under `expect`, so tests can't drive it.
- **Lifecycle hooks mark the host.** `claude stop` fires SessionEnd, waking with `--resume` fires SessionStart with `source: "resume"`, and Notification fires after about a minute idle.

## The design

**1. Two owners.** Every session is either the desk's or Emil's (`owner` on the Session, see Primitives); a track counts as the desk's while its live session is.

- *The desk's*: a track the desk started (`tracks start` from the desk), or one Emil handed it ("desk, take bun-rollout"). The desk briefs it, answers its questions, sends it back, and brings Emil only its decisions.
- *Emil's*: every track Emil started or typed into. The desk never types into it, never answers its asks and never nudges it. It shows the track in the portfolio and in "what's up?", read from the card.

**2. Reports push, the desk does not watch.** A desk track reports through the machinery lead mode already has: `parent:` on the card, the Stop hook, `tracks wait` on the parent. The desk's Monitor follows only its own children, so an event is always about work it owns. Emil's tracks reach the desk only as card changes for the portfolio view, and never as events to act on.

**3. Emil's first prompt takes a track back.** The UserPromptSubmit hook (the attention log already runs there) sets `owner: emil` when a prompt arrives that the desk did not type. The desk is told once, and the track leaves its watch. Giving a track back to the desk is a sentence in the desk's chat. This is Superego's rule, and it is what removes take-back locks: a track has one owner at a time, so nobody needs to tell two voices apart.

**4. Notes wait for idle.** `tracks send` to a busy desk track holds one note on the card and delivers it at the track's next Stop. A second send merges into the held note or replaces it (`--replace`). The send is never refused and never typed into a busy pane.

**5. Desk tracks stop before shipping.** As threads do in lead mode, a desk track stops at a commit or an open PR. Merging, installing and deploying are the desk's to do by the track's tier, or Emil's to answer. The rule follows from what a desk track is, so the word lists that sort asks into questions and decisions are no longer needed.

**6. Caps.** At most 4 desk tracks run at once, and further starts queue. Emil's own focus stays at 2. The first cap limits what the desk runs, the second limits Emil's attention.

**7. Decisions are records, and answering them is one gesture.** See Answering below.

**8. The desk is headless.** The pane is one client among several. See Headless below.

**Kept from v2:** the conversation, the portfolio (`rank:`, `today:`, `blocked_by:`, the morning chat and the end-of-day review), the handled log, the Needs you band (EMI-375) for the desk's decisions, "what tracks write is data", and quiet on events with a numbered open list (memory: desk-quiet-on-events). The desk stays in Rex: the limit that matters is "the desk acts only on what it owns", not where it runs.

## Answering

What reaches Emil today arrives two ways that don't know about each other. The desk's chat has a numbered "open with you" list. The pane's Needs you band shows each track's asks, and `1`-`9` there opens a reply to the agent prefilled with ask N's text. An ask is a line of free text whose number can change when the card is rewritten (store.md, P1). Emil reads the question, the recommendation and the evidence in different places, and answers by typing.

Superego's board answers in one keystroke: decisions have options, `1`-`9` picks one, `z` takes it back within 3 seconds, and `r` answers in words. v3 does the same:

- **A decision is a record** in `tracks.db`: an id (`D12`), its track, the question, up to 9 options with one marked as recommended, evidence lines (each a fact with its source: CI, review, size, freeze), links, when it was asked, and its answer (text, by whom, when). Asks from Emil's own tracks become records too, so there is one list, not two.
- **One list everywhere.** The band, the desk's chat and any later app show the same decisions under the same ids. An answer given in one place shows as answered in the others. In chat, "D12: 2" or "D12 yes" works.
- **The pane answers in one key.** In the band, `enter` takes the recommendation, `1`-`9` picks an option and `r` answers in words. The answer waits 5 seconds before it is sent, the row shows a countdown, and `z` takes it back. A decision row expands to its evidence and links (`space`), and `o` opens its PR.
- **Answers go to the owner.** An answer to a desk track goes to the desk, which acts on it. An answer to Emil's own track goes into that track as a held note (design point 4), so it never types into a busy agent.
- **Asking doesn't block.** The desk keeps working on everything that doesn't depend on the answer.
- **Answers to one asker go together (EMI-387).** An asker with more than one open question has each answer staged (shown as staged, changeable) and sent as one message after one 5-second undo, when its last question is staged or on "Send N answers" (⌘↩), so its agent is not woken between answers. One open question sends as before.
- **A track's decisions are one dialog (EMI-424).** A track's open decisions, its agent's asks and the ones the desk raised about it, show together in one card above the desk field, other decisions first and then merge, install and close, since one can bear on another. Each shows its options; 1–9 and return act on the first one still undecided and move on as each is staged. The answers go after one undo window, as one message per asker: the agent gets its answers together, the desk its own.
- **Ask before answering (EMI-387).** "Ask for more…" (`m`) tells the agent what Emil wants to know and leaves the question open; "Show me the options" asks it to render each option to an image. Both are `POST /v1/questions/{id}/ask`.
- **The desk is always one keystroke away (EMI-387).** The composer always messages the desk; the selected question is a card above it, and ⌘L puts the cursor in the desk field. Digits and enter act on the question only when no text field has the cursor.
- **Fewer, better decisions.** A decision without options and a recommendation is refused at write time (`tracks decision add` checks), so an open-ended question becomes a chat message, not a row.

## Headless

The desk should run without the Rex pane, so a native app (or Meridian, which already reads `tracks ls --json`) can be its face later. Superego's principle applies: a headless daemon holds the state, the UI is a client, and clients render state rather than replaying events.

Two layers, built in this order:

1. **State and actions behind the CLI (in v3).** `tracks.db` is already the source of truth. Every read the pane makes gets a `--json` command, every action a command, and the pane uses only those:
   - `tracks decisions [--json]`, `tracks decide ID (N | --text '…') [--by emil|desk]`, `tracks decision add|close`;
   - `tracks watch --json`: a snapshot, then one JSON line per change (tracks, decisions, handled log), for any client to follow;
   - the portfolio (`rank:`, `today:`, `blocked_by:`) and the handled log, as they are, with `--json`.
   
   A native app then needs no Rex to show the portfolio and the decisions, or to answer them. "Open the track" stays a Rex action, because the tracks run in Rex.
2. **The desk's conversation (later, when a native app is built).** Today the desk is an interactive Claude Code session in a Rex pane, which gives it Claude Code's UI, auto mode and the Monitor tool for free. A headless desk would run under a small supervisor (`tracks desk serve`) driving Claude Code with `claude -p --input-format stream-json --output-format stream-json --resume`, or the Agent SDK, and keep the chat in `tracks.db` for any client to show. Not verified yet: which of auto mode, Monitor and permission prompts work the same in that mode. Building this before there is a second client would be speculative, so v3 only keeps the door open:
   - the desk skill uses `tracks` commands and never `rex` directly, so it doesn't care what hosts it;
   - everything the desk raises for Emil is a decision record or a handled-log entry, never chat-only state.

## What we can delete

Once desk tracks report by push and Emil's tracks are off limits, these go or shrink:

| v2 piece | After v3 |
|---|---|
| Screen reading for stalls (`tracks screen` in the desk skill) | Only for an exited agent or a permission prompt on a desk track |
| `tracks desk answer` into any track, on any tier (EMI-372) | Desk tracks only. Emil answers his own tracks where he works |
| Question-or-decision rules and the decision word lists (EMI-363, EMI-369) | Gone: desk tracks stop before shipping, Emil's tracks are not the desk's to answer |
| `tracks ask --wait` and its Stop matching (EMI-371) | Desk tracks: send a note, the answer comes with the next report. Emil's tracks: read the card |
| Refusing to type into a busy agent, retry later | The held note (design point 4) |
| `u` take-back and its locks (EMI-363) | The handled log stays as a record; ownership replaces the locks |
| `[desk]` passing Emil's decision into his own tracks | Only into desk tracks |
| Monitor on `tracks wait --all --follow` | Monitor on the desk's own children |

Count the code each row removes before building, and hold v3 to a net reduction in lines.

## How we build it

Faster and lighter than v2 (Emil, 2026-11-24): v2's slices each took two or three adversarial review rounds, mostly on edge cases nobody had hit in use. v3 is built by the rules in this repo's `CLAUDE.md`. Pane and skill changes merge after a self-read. Code that types into agents or writes `tracks.db` gets one review round, which acts only on what is hard to undo. Each slice is used for a day before the next, and what felt wrong becomes the next slice.

## Steps

Changed 2026-11-24 after the mockups (artifact "Desk v3: UI mockups"): Emil picked the native app (B + native) as the target and dropped A. In A the desk is an interactive Claude Code session, so every event the Monitor delivers starts a turn in the same chat Emil types in: a question scrolls away while he answers it, and a report lands while he types a new task. That is the v2 problem (memory desk-quiet-on-events) and A can't fix it, because Claude Code owns that chat. When tracks owns the conversation, events land on the board and wait in a queue, and the composer never moves.

0. Done: Step 0 probing (results above).
1. Done (EMI-376): schema 8 with Session, Question and Message, status from hooks, `tracks sessions`. `owner` and the hand-back rule are in; the desk sets `owner: desk` from step 2.
2. The desk starts its sessions with `claude --bg`, `parent` = the desk's session, and Stop-hook verify. Reports become Messages to the desk. Cap 4, with a queue.
3. Messages delivered at Stop (stop + resume), held and merged while a session is busy. Permission prompts of desk sessions become Questions through a waiting PermissionRequest hook.
4. Questions as records: `tracks decision add|close`, `tracks decide`, `tracks decisions --json`, one id everywhere.
5. Done (EMI-380): `tracks serve`: the headless desk and the API (docs/serve-api.md, schema 11). (Probed 2026-11-24: `claude -p --input-format stream-json --output-format stream-json --verbose` runs in auto mode, loads skills (the desk skill included), MCP servers and hooks, and keeps one process across turns. A message written while a turn runs is folded into that turn, so serve must hold messages until the turn's `result`. Hooks there see `CLAUDE_CODE_ENTRYPOINT=sdk-cli`, so `tracks hook` skips them and serve writes the desk's session row itself.) It runs the desk through Claude Code's stream-json mode, keeps its chat in `tracks.db`, and decides when to wake it: events queue while Emil types and reach the desk at the end of its turn, as a batch. It serves state as a snapshot plus changes. Check first that auto mode, skills and hooks behave in stream-json mode as they do interactively.
6. The native app (SwiftUI): sessions | conversation | board, as in the mockup, a client of `tracks serve` only. Answering with one key, a 5-second undo, and `t`/`g` to take over and give back. Meridian keeps the one menu bar count and links into it.
7. Delete what v3 replaced (the table under What we can delete), after a week of use.

The old tracks pane keeps working until step 6 is in daily use.

## Open questions for Emil

1. **Lead mode.** With the desk owning its tracks, a lead is the same pattern one level down. Recommend: keep `/lead` for one large goal, and let the desk start a lead track for one, rather than merging the two.
2. **What takes a track back.** Any prompt Emil types (Superego's rule, my pick), or only an explicit "take it"? Any prompt is simpler, and a sentence to the desk gives the track back.
3. **Today's open tracks.** All start as Emil's, and he hands over the ones he wants the desk to run (my pick). Or the desk adopts every background track.
4. **The cap.** 4 running desk tracks, as Superego does, or fewer to start with?
5. **What bothers you most when answering today?** The design above guesses: answers spread over two places, typing instead of picking, and evidence not next to the question. Name what it misses.
6. **Accept all recommendations.** One key (with a confirm) that takes every open decision's recommendation in one go. Fast for rounds, but it invites approving without reading, which the proposal's end-of-day question watches for. My pick: not in v3.

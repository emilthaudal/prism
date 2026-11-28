#!/usr/bin/env python3
"""Writes stress fixtures for Prism's window, on made-up names only (the repo is public).

    scripts/stress-fixture.py OUTDIR

OUTDIR gets three folders, each a serve fixture with its serve-transcripts.json beside it (FixtureClient reads both):
  big/    40 tracks, 120 sessions with subagents, 30 open questions, 2000 chat messages (154 of them Emil's) with long Markdown and code
  worst/  very long unbroken names, one-letter names, emoji and non-Latin text, huge counts, missing optional fields
  empty/  zero items everywhere
plus big/replay.json, a demo script that drops a report into the chat every half second (--demo-script).
"""
import json
import os
import sys

T0 = "2026-11-24T13:32:00Z"
DESK = "c45dc3eb-5f0e-4d6a-9a8e-1b2c3d4e5f60"
WORDS = ["pantry", "recipe", "search", "queue", "label", "invoice", "carrier", "basket", "refund", "scanner", "export",
         "rollout", "metrics", "brief", "stack", "sync", "deploy", "bot", "cache", "gateway"]
PEOPLE = ["Alex", "Sam"]
MODES = ["focus", "focus", "background", "background", "background", "waiting", "parked"]
STATUSES = ["working", "working", "working", "done", "idle", "blocked", "stopped", "error"]
# About one message in thirteen is Emil's, as in a long real conversation (153 of 1,870).
EMIL_EVERY = 13
DOT, MINUS = chr(0xB7), chr(0x2212)


def u(*codes):
    return "".join(chr(c) for c in codes)


def at(minutes_ago):
    total = (13 * 60 + 32 - minutes_ago) % (24 * 60)
    return "2026-11-24T%02d:%02d:00Z" % (total // 60, total % 60)


def uid(n):
    return "%08x-0000-4000-8000-%012x" % (0x5000000 + n, n)


def name(i):
    return "%s-%s" % (WORDS[i % len(WORDS)], WORDS[(i * 7 + 3) % len(WORDS)])


MD = """Here is what changed in **{n}**:

- moved the `{w}` handler behind the gateway
- kept the old route for one release
- `bun test` passes on all shards

```swift
func {w}(_ items: [Item]) -> [Item] {{
    items.filter {{ $0.isOpen && !$0.isQueued }}.sorted {{ $0.rank < $1.rank }}
}}
```

| check | result |
|---|---|
| CI | green |
| review | Alex |

A long paragraph that keeps going so the line wraps several times in the conversation column: the desk read the diff, the tests and the
review, found nothing that blocks, and recommends merging after Sam has looked at the label spec, which he said he would do today."""


def big():
    tracks, sessions, questions, conversation, handled, transcripts = [], [], [], [], [], {}
    sessions.append({"id": DESK, "short": DESK[:8], "owner": "desk", "host": "background", "name": "desk", "status": "working",
                     "activity": "Reading reports", "updated_at": T0})
    for i in range(40):
        tid = str(200 + i)
        mode = MODES[i % len(MODES)]
        tracks.append({"id": tid, "name": name(i), "goal": "Ship %s for PAN-%d" % (name(i), 100 + i), "mode": mode, "tier": str(1 + i % 3),
                       "rank": i + 1, "today": "step %d of 5" % (i % 5 + 1), "where": "Wave %d" % (i % 3 + 1),
                       "waiting_on": PEOPLE[i % 2] if mode == "waiting" else "", "links": ["https://github.com/acme/%s/pull/%d" % (name(i), 400 + i)],
                       "blocked_by": [], "updated_at": at(i)})
    for j in range(120):
        tid = str(200 + j % 40)
        sid = uid(j)
        status = STATUSES[j % len(STATUSES)]
        subs = [{"name": "%s sweep %d" % (WORDS[(j + k) % len(WORDS)], k), "status": "working" if k % 2 == 0 else "done"} for k in range(j % 4)]
        sessions.append({"id": sid, "short": sid[:8], "track": tid, "parent": DESK, "owner": "emil" if j % 9 == 0 else "desk", "host": "background",
                         "name": "%s-%d" % (name(j % 40), j), "status": status,
                         "waiting_for": "permission" if status == "blocked" else "",
                         "activity": "Bash: bun test --shard %d/3" % (j % 3 + 1) if status == "working" else "",
                         "subagents": subs, "last_answer": "Shard %d passed. The branch is ready for review." % (j % 3 + 1),
                         "changes": {"added": j * 13, "removed": j * 5, "files": j % 17 + 1, "branch": "ops-%d/%s" % (j, name(j % 40)), "merged": j % 11 == 0},
                         "seen_at": None if j % 3 else at(1), "updated_at": at(j % 60)})
        transcripts[sid] = {"entries": [{"role": "user" if k % 5 == 0 else "assistant", "text": MD.format(n=name(j % 40), w=WORDS[k % len(WORDS)]), "steps": k % 9,
                                         "at": at(200 - k)} for k in range(60 if j < 3 else 6)]}
    for q in range(30):
        j = q * 4 + 1
        questions.append({"id": "D%d" % (100 + q), "track": str(200 + j % 40), "session": uid(j), "from": "agent" if q % 2 else "desk",
                          "question": "Merge PR #%d in %s now, or wait for Sam's review of the label spec?" % (500 + q, name(j % 40)),
                          "options": [{"label": "Merge now, squash", "description": "CI green %s +%d %s%d in %d files" % (DOT, q * 10, MINUS, q, q % 9 + 1)},
                                      {"label": "Wait for Sam", "description": "He owns the label spec. Back at 15:00."},
                                      {"label": "Hold until tomorrow", "description": "Ships with the 09:00 release train."}],
                          "recommended": q % 3, "evidence": [{"text": "CI green", "source": "CircleCI #%d" % (48000 + q)}, {"text": "No code freeze", "source": "Gatekeeper"}],
                          "links": ["https://github.com/acme/%s/pull/%d" % (name(j % 40), 500 + q)], "created_at": at(q), "answer": None})
    for m in range(2000):
        role = "emil" if m % EMIL_EVERY == 0 else ["desk", "desk", "report"][m % 3]
        e = {"id": "m%05d" % m, "at": at(2000 - m), "role": role}
        if role == "emil":
            e["text"] = "take %s, one session per repo, stop at PRs" % name(m % 40)
        elif role == "report":
            j = m % 120
            e.update(session=uid(j), track=str(200 + j % 40), outcome=["done", "blocked", "error", "done"][m % 4], text=MD.format(n=name(j % 40), w=WORDS[m % len(WORDS)]))
        else:
            extra = ("NLNLD%d and D%d need you." % (100 + m % 30, 101 + m % 29)).replace("NL", chr(10)) if m % 8 == 1 else ""
            e.update(text=MD.format(n=name(m % 40), w=WORDS[m % len(WORDS)]) + extra, steps=m % 13)
        conversation.append(e)
    for h in range(200):
        handled.append({"id": "e%04d" % h, "at": at(h), "track": str(200 + h % 40), "kind": ["nudged", "answered", "started", "closed"][h % 4],
                        "did": "Reminded %s that the review was due" % name(h % 40)})
    snap = {"seq": 5000, "generated_at": T0, "desk": {"session": DESK, "status": "working", "queued_events": 0,
            "usage": {"five_hour": {"used_percentage": 87, "resets_at": "2026-11-24T15:40:00Z"},
                      "seven_day": {"used_percentage": 64, "resets_at": "2026-11-30T08:00:00Z"}, "updated_at": T0}},
            "tracks": tracks, "sessions": sessions, "questions": questions, "conversation": conversation, "handled": handled}
    steps = []
    for k in range(20):
        j = (k * 7) % 120
        steps.append({"name": "report %d" % k, "at": k * 0.5, "do": "event", "event": {"op": "upsert", "kind": "conversation", "value": {
            "id": "r%03d" % k, "role": "report", "session": uid(j), "track": str(200 + j % 40), "outcome": "done", "text": MD.format(n=name(j % 40), w="replay")}}})
    return snap, transcripts, {"lead": 3, "tail": 2, "steps": steps}


LONG = "refund-rules-for-b2b-carrier-labels-in-the-pantry-api-gateway-and-the-recipe-search-indexer-without-any-break-at-all"
ROCKET, FIRE, TUBE, FAMILY = u(0x1F680), u(0x1F525), u(0x1F9EA), u(0x1F468, 0x200D, 0x1F469, 0x200D, 0x1F467)
NAMES = [
    LONG,
    "x",
    u(0x98DF, 0x54C1, 0x5EAB) + "-" + u(0x691C, 0x7D22, 0x30EA, 0x30C9, 0x30EB, 0x30C4, 0x30FC),  # Japanese
    ROCKET + FIRE + " rocket-" + TUBE + " lab " + FAMILY,
    u(0x0645, 0x062E, 0x0632, 0x0646) + "-" + u(0x0627, 0x0644, 0x0628, 0x062D, 0x062B),  # Arabic, right to left
    u(0x0440, 0x0435, 0x0446, 0x0435, 0x043F, 0x0442) + "-" + u(0x043F, 0x043E, 0x0438, 0x0441, 0x043A),  # Cyrillic
]


def worst():
    tracks, sessions, questions, conversation = [], [], [], []
    sessions.append({"id": DESK, "owner": "desk", "status": "working"})
    for i, n in enumerate(NAMES):
        tid = str(300 + i)
        t = {"id": tid, "name": n, "mode": ["focus", "focus", "background", "waiting", "parked", "background"][i]}
        if i % 2 == 0:
            t.update(goal=n + " " + LONG, today=LONG, where=LONG, rank=i)
        if t["mode"] == "waiting":
            t["waiting_on"] = "Sam and Alex and everyone on the " + LONG
        tracks.append(t)
        sid = uid(900 + i)
        s = {"id": sid, "track": tid, "owner": "desk", "status": ["working", "blocked", "error", "done", "working", "idle"][i], "name": n}
        if i % 2 == 0:
            s.update(activity="Bash: " + LONG + " && " + LONG, waiting_for="permission to run " + LONG,
                     subagents=[{"name": LONG, "status": "working"}, {"name": TUBE, "status": "done"}],
                     changes={"added": 9999999, "removed": 8888888, "files": 123456, "branch": LONG}, last_answer=LONG)
        sessions.append(s)
        q = {"id": "D%d" % (900 + i), "track": tid, "session": sid, "question": (n + "? ") * (8 if i == 0 else 1)}
        if i % 2 == 0:
            q.update(options=[{"label": LONG, "description": LONG + " " + LONG}, {"label": "x"}, {"label": ROCKET}], recommended=0)
        else:
            q.update(recommendation=LONG)
        questions.append(q)
    # A track with nothing but an id, a name and a mode, and no session.
    tracks.append({"id": "399", "name": "y", "mode": "focus"})
    bodies = [None, None, "", ROCKET * 300, "`" + LONG * 3 + "`", "|a|b|NL|-|-|NL|".replace("NL", chr(10)) + LONG + "|x|"]
    for m, n in enumerate(NAMES * 3):
        role = ["emil", "desk", "report"][m % 3]
        body = bodies[m % 6]
        e = {"id": "w%03d" % m, "role": role, "text": n if m % 6 == 0 else (n * 4 if body is None else body)}
        if role == "report":
            e.update(session=uid(900 + m % 6) if m % 2 else "gone-session", outcome=[None, "done", "weird-outcome"][m % 3])
            if m % 2 == 0:
                e["track"] = "300"
        conversation.append(e)
    snap = {"seq": 2147483000, "desk": {"session": DESK, "status": "working", "queued_events": 99999,
            "usage": {"five_hour": {"used_percentage": 100, "resets_at": "2026-11-24T15:40:00Z"}, "seven_day": {"used_percentage": 1000}}},
            "tracks": tracks, "sessions": sessions, "questions": questions, "conversation": conversation,
            "handled": [{"id": "h%d" % k, "did": LONG} for k in range(5)]}
    return snap, {uid(900): {"entries": [{"role": "assistant", "text": LONG * 20}, {"role": "user", "text": ""}]}}


def write(out, name_, snap, transcripts, replay=None):
    d = os.path.join(out, name_)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "fixture.json"), "w") as f:
        json.dump(snap, f, ensure_ascii=False)
    with open(os.path.join(d, "serve-transcripts.json"), "w") as f:
        json.dump(transcripts, f, ensure_ascii=False)
    if replay:
        with open(os.path.join(d, "replay.json"), "w") as f:
            json.dump(replay, f)
    print(os.path.join(d, "fixture.json"))


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else "build/stress"
    snap, transcripts, replay = big()
    write(out, "big", snap, transcripts, replay)
    snap, transcripts = worst()
    write(out, "worst", snap, transcripts)
    write(out, "empty", {"seq": 1, "desk": {}}, {})


if __name__ == "__main__":
    main()

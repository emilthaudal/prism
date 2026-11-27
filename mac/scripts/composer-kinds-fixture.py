#!/usr/bin/env python3
"""Derives the fixture for the composer's three kinds of decision from mock/serve-fixture.json (made-up names only):
D16 a picture choice (the four icons in docs/fixtures/icons), D19 a decision with no options and no recommendation,
D20 a decision with no options and a recommendation in words, each on a track of its own so none joins a dialog.
Usage: composer-kinds-fixture.py SOURCE_FIXTURE ICONS_DIR OUT_FIXTURE"""
import json
import sys

source, icons, out = sys.argv[1:4]
d = json.load(open(source))

pictures = [("owl looking back", "owl-looking-back.png"), ("U-turn parcel", "u-turn-parcel.png"),
            ("round trip", "round-trip.png"), ("mended parcel", "mended-parcel.png")]
base = {"track": "114", "session": None, "from": "agent", "evidence": [], "links": [], "attachments": [],
        "created_at": "2026-11-24T13:20:00Z", "answer": None, "recommended": None}

visual = dict(base, id="D16", track="121", question="Which icon for the Pantry Workspace?", recommended=0,
              recommendation="owl looking back, the parcel owl's sibling.",
              options=[{"label": label, "description": "",
                        "attachment": {"label": name, "url": "file://%s/%s" % (icons, name), "kind": "image"}}
                       for label, name in pictures])
open_ended = dict(base, id="D19", track="108", question="What should the refund-rules branch be called?", options=[])
worded = dict(base, id="D20", track="102", question="How should the pantry-api release notes read?", options=[],
              recommendation="Short and plain: one line per change, newest first.")

d["questions"] = [visual, open_ended, worded] + d["questions"]
json.dump(d, open(out, "w"), indent=2)

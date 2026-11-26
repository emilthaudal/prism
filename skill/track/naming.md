# Naming a track

Read this on the first turn of a `Not started.` track, before you write anything on the card.

If the card has `named_by: desk`, the desk chose NAME from its whole conversation with Emil: keep it and skip the rest of this file. Otherwise the pane cut NAME out of Emil's words by rule, so treat it as a placeholder. Read the request (and glance at the repo or ticket it points to) until you know what the work is, then name it yourself: the subject a stranger would recognise in the pane a week from now, not the way he phrased the ask.

- Lead with the thing (a ticket key, service, repo or feature), then what about it, such as `pan-213-refund-retries`, `gatekeeper-guided-reviews` or `claude-code-verification`.
- Leave out how he asked and how he feels (`make`, `how`, `see`, `still`, `very`, `prep`) and words every track shares (`track`, `request`, `task`).
- Make it specific enough to tell apart from his other tracks: `metrics-meeting-prep`, not `meeting-prep`.
- Lowercase words joined by `-`, at most 4 words and 32 characters, a ticket key kept whole.

Keep NAME only when it is already the name you would have picked. Otherwise run `tracks rename NAME BETTER` once. It moves the card to `cards/BETTER.md` and relabels the Rex session, and from then on BETTER is the track's name everywhere: every command and your replies. Say so in one line: `Renamed the track to BETTER.` If it fails (the name is taken, say), keep NAME and say why in that line.

Rename only your own track: on this first turn by your own judgment, and later only when Emil asks, since the name is how he finds the track.

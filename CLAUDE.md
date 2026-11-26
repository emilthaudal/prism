# tracks: how we build it

Emil decided on 2026-11-24 to build tracks and the desk with more risk and faster iteration: fewer adversarial reviews, and more judging by how it feels in use. This overrides the tier process in `~/.claude/CLAUDE.md` for this repo only.

- **Pane, skills, prompts, docs, keys, wording:** tier 1. Make the change, read your own diff, merge, and Emil uses it.
- **Code that types into agents, writes `tracks.db` or runs unattended (hooks, launchd, resume):** one read-only review round. Act-on covers only what is hard to undo: losing or corrupting `tracks.db`, the desk acting on a track it doesn't own, or anything that reaches outside tracks (a merge, a deploy, a push, another repo). Every other finding is a note in the PR body, not a fix round. No second round, and no probing end-to-end runs.
- **Schema migrations of `tracks.db`:** take a verified backup first, as `tracks migrate` does. That backup is the safety net, not more review.
- **Slices are small and fix-forward.** Merge, use it for a day, note what felt wrong, and make that the next slice. A revert is cheap; a week of hardening before anyone used it is not.
- **Don't harden edge cases nobody has hit.** Fix a case when it happens in use, not when a reviewer imagines it.

**Who merges.** Desk sessions stop at a commit on their branch. The desk merges a finished branch into `main` itself, when the rules above let that work merge, and pushes `main` to `origin` (Emil, 25 Nov). It then installs `main` (`make install install-skills`, which restarts `prism server`) once Emil has said sessions persist through a restart (until then installs stay asks), when none of its sessions is working or blocked, no standing order holds installs back, and nothing since the installed build changes the schema of `tracks.db`; it closes its own finished tracks, which Emil reopens with `u` in the handled log (EMI-415). Installs that migrate `tracks.db`, Prism.app, and anything Emil starts himself stay his.

The structural guards stay and do the safety work instead of review: desk tracks stop at a commit or a PR, confirm pages, GitHub branch protection, and auto mode.

#!/bin/sh
# Captures Prism's window from a fixture without touching the screen: launched with open -g in the background (no Dock
# icon, never active), captured by window id, quit. Usage: scripts/shots.sh FIXTURE OUTDIR
set -eu
fixture=$1
out=$2
here=$(cd "$(dirname "$0")" && pwd)
app="$(cd "$here/.." && pwd)/build/Prism.app"
mkdir -p "$out"

shot() { # name appearance [app arguments]
	name=$1
	appearance=$2
	shift 2
	open -g -n "$app" --args --fixture "${FIXTURE_OVERRIDE:-$fixture}" --appearance "$appearance" --background "$@"
	pid=""
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		sleep 0.5
		pid=$(pgrep -n -f "$app/Contents/MacOS/Prism" || true)
		[ -n "$pid" ] && break
	done
	[ -n "$pid" ] || { echo "Prism did not start" >&2; exit 1; }
	sleep "${SETTLE:-3}"
	id=$(swift "$here/window-id.swift" "$pid")
	screencapture -x -o -l "$id" "$out/$name.png"
	kill "$pid"
	echo "$out/$name.png"
}

# The README's pictures, on made-up data only (docs/readme/fixture.json), since the repo is public.
if [ "${ONLY:-}" = readme ]; then
	shot prism-light light
	shot prism-dark dark
	exit 0
fi

if [ "${ONLY:-}" != sidebar ]; then
shot desk-light light
shot desk-dark dark
shot session-dark dark --select pantry-api
SETTLE=1 shot countdown-light light --demo-answer
fi

# The sidebar's nesting: a track with 2 sessions, one with 2 subagents, activity while working.
if [ -z "${ONLY:-}" ] || [ "${ONLY:-}" = sidebar ]; then
	FIXTURE_OVERRIDE="$(dirname "$fixture")/serve-fixture-sidebar.json"
	export FIXTURE_OVERRIDE
	shot sidebar-light light
	shot sidebar-dark dark
	unset FIXTURE_OVERRIDE
fi
[ "${ONLY:-}" = sidebar ] && exit 0

# The board's quiet state: no open questions, so the day's digest and a brief per track.
export FIXTURE_OVERRIDE="$(dirname "$fixture")/serve-fixture-quiet.json"
shot board-quiet-light light
shot board-quiet-dark dark
unset FIXTURE_OVERRIDE

# A batch: the fixture's recipe-search has three open questions; two are staged, the third shows on the card above the desk field.
export FIXTURE_OVERRIDE="$(dirname "$fixture")/serve-fixture-batch.json"
shot batch-staged-light light --select D18 --demo-stage
shot batch-staged-dark dark --select D18 --demo-stage
unset FIXTURE_OVERRIDE

# A visual choice: the fixture's icon question, its pictures in docs/fixtures/icons.
icons=$(cd "$out" && pwd)/fixtures/icons
visual=$(mktemp -t desk-icons).json
sed "s#__ICONS__#$icons#g" "$(dirname "$fixture")/serve-fixture-icons.json" > "$visual"
export FIXTURE_OVERRIDE=$visual
shot composer-icons-light light --select D16
shot composer-icons-dark dark --select D16
rm -f "$visual"

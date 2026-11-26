#!/bin/sh
# Plays a demo script on the fixture once per appearance and takes the still each step asks for, in the background like
# shots.sh: open -g, --background (never active or key), screencapture by window id, quit. State is set inside the app.
# check compares each still with its baseline (ui/baselines/APPEARANCE/NAME.png) and exits 1 on a difference; baseline
# rewrites the baselines from this run, on purpose.
# Usage: scripts/ui-check.sh check|baseline FIXTURE SCRIPT OUTDIR
set -eu
mode=$1
fixture=$2
script=$3
out=$4
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
app="$root/build/Prism.app"
baselines="$root/ui/baselines"
motion="$(cd "$root" && swift build -c release --show-bin-path)/prism-motion"
# A step's still must come back within this many tenths of a second of the app asking, or the run is over.
limit=1800
status=0

for appearance in light dark; do
	dir="$out/$appearance"
	rm -rf "$dir"
	mkdir -p "$dir"
	open -g -n "$app" --args --fixture "$fixture" --appearance "$appearance" --background --demo-script "$script" --ui-dir "$dir"
	pid=""
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		sleep 0.5
		pid=$(pgrep -n -f "$app/Contents/MacOS/Prism" || true)
		[ -n "$pid" ] && break
	done
	[ -n "$pid" ] || { echo "Prism did not start" >&2; exit 1; }
	# The window's id first, then the go-ahead: the script starts only once a still can be taken without delay.
	id=""
	for _ in $(seq 1 50); do
		id=$(swift "$here/window-id.swift" "$pid" 2>/dev/null || true)
		[ -n "$id" ] && break
		sleep 0.2
	done
	[ -n "$id" ] || { echo "no window for Prism" >&2; kill "$pid" 2>/dev/null || true; exit 1; }
	touch "$dir/recording"
	ticks=0
	while [ ! -f "$dir/done" ] && kill -0 "$pid" 2>/dev/null && [ "$ticks" -lt "$limit" ]; do
		for request in "$dir"/*.request; do
			[ -e "$request" ] || continue
			name=$(basename "$request" .request)
			rm -f "$request"
			screencapture -x -o -l "$id" "$dir/$name.png"
		done
		sleep 0.1
		ticks=$((ticks + 1))
	done
	kill "$pid" 2>/dev/null || true
	[ -f "$dir/done" ] || { echo "$appearance: the script did not finish" >&2; status=1; continue; }

	if [ "$mode" = baseline ]; then
		mkdir -p "$baselines/$appearance"
		for png in "$dir"/*.png; do
			case "$png" in *.diff.png|*.side.png) continue ;; esac
			cp "$png" "$baselines/$appearance/"
		done
		echo "$appearance: baselines written to $baselines/$appearance"
	else
		"$motion" ui-compare "$dir" "$baselines/$appearance" "$script" "$appearance" || status=1
	fi
done
exit $status

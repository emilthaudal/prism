#!/bin/sh
# Records Prism's window while a demo script plays on the fixture, then checks each step's motion. In the background
# like shots.sh: open -g, --background (never active or key), ScreenCaptureKit filtered to the window by id, quit.
# Usage: scripts/motion.sh FIXTURE SCRIPT OUTDIR [app arguments, such as -reduceMotion YES]
set -eu
fixture=$1
script=$2
out=$3
shift 3
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
app="$root/build/Prism.app"
motion="$(cd "$root" && swift build -c release --show-bin-path)/prism-motion"

rm -rf "$out"
mkdir -p "$out"
open -g -n "$app" --args --fixture "$fixture" --appearance light --background --demo-script "$script" --motion-dir "$out" "$@"
pid=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
	sleep 0.5
	pid=$(pgrep -n -f "$app/Contents/MacOS/Prism" || true)
	[ -n "$pid" ] && break
done
[ -n "$pid" ] || { echo "Prism did not start" >&2; exit 1; }
trap 'kill "$pid" 2>/dev/null || true' EXIT
"$motion" record "$pid" "$out"
kill "$pid" 2>/dev/null || true
"$motion" check "$out"

#!/bin/sh
# Stills of the composer's other kinds of decision, in light and dark, in the background: a picture choice (D16), a
# decision with no options (D19), one with only a recommendation in words (D20) and Ask for more. The fixture is the
# sample's with those three added (composer-kinds-fixture.py, made-up names). Stills land in build/composer-kinds/APPEARANCE;
# there are no baselines for them, so the comparison's lines are not a result.
# Usage: scripts/composer-kinds.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
out="$root/build/composer-kinds"
mkdir -p "$out"
python3 "$here/composer-kinds-fixture.py" "$root/../mock/serve-fixture.json" "$root/docs/fixtures/icons" "$out/fixture.json"
"$here/ui-check.sh" check "$out/fixture.json" "$here/ui-composer-kinds.json" "$out" || true

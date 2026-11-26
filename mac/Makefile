APP := build/Prism.app
FIXTURE ?= ../mock/serve-fixture.json

# A stable signing identity keeps macOS's notification grant across rebuilds; an ad-hoc signature is a new app to
# macOS each build. Uses the first "Apple Development" identity, else ad hoc.
SIGN_IDENTITY ?= $(shell security find-identity -v -p codesigning 2>/dev/null | grep -m1 -o '"Apple Development: [^"]*"' | tr -d '"')

SCRIPT ?= scripts/motion-sample.json
MOTION_OUT ?= build/motion
UI_SCRIPT ?= scripts/ui-sample.json
UI_OUT ?= build/ui

.PHONY: build test app icon install run demo shots readme-shots motion motion-arrive motion-composer ui-check ui-baseline clean

build:
	swift build

test:
	swift test

# Assembles build/Prism.app around the release binary.
app:
	swift build -c release
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp "$$(swift build -c release --show-bin-path)/Prism" $(APP)/Contents/MacOS/Prism
	cp Resources/Info.plist $(APP)/Contents/Info.plist
	mkdir -p $(APP)/Contents/Resources
	# Absolute paths: actool runs in a long-lived helper that resolves relative paths against its own folder,
	# which put the icon in another checkout's build/ and left this app without one.
	xcrun actool $(CURDIR)/Resources/Prism.icon --compile $(CURDIR)/$(APP)/Contents/Resources --platform macosx \
		--minimum-deployment-target 26.0 --app-icon Prism --output-partial-info-plist $(CURDIR)/build/icon-info.plist >/dev/null
	@test -f $(APP)/Contents/Resources/Assets.car || { echo "actool wrote no icon into $(APP)" >&2; exit 1; }
	@if [ -n "$(SIGN_IDENTITY)" ]; then \
		codesign --force --sign "$(SIGN_IDENTITY)" $(APP) && echo "signed with $(SIGN_IDENTITY)"; \
	else \
		codesign --force --sign - $(APP) >/dev/null 2>&1 || true; echo "signed ad hoc (no Apple Development identity)"; \
	fi
	@echo "built $(APP)"

# Redraws the art of the app icon, "The split", into Resources/Prism.icon: scripts/icon.mjs writes one SVG per layer,
# WebKit renders them offscreen (the blur filters need a browser engine), and Icon Composer's ictool draws the finished
# icon for the README. Commit the PNGs; make app compiles the .icon with actool.
icon:
	mkdir -p build Resources/Prism.icon/Assets
	node scripts/icon.mjs build/icon
	swift scripts/render-svg.swift $(foreach l,glows light prism,build/icon/$(l).svg 1024 Resources/Prism.icon/Assets/$(l).png)
	"$$(xcode-select -p)/../Applications/Icon Composer.app/Contents/Executables/ictool" Resources/Prism.icon --export-image \
		--output-file docs/icon.png --platform macOS --rendition Default --width 256 --height 256 --scale 2 >/dev/null

# Installs build/Prism.app as ~/Applications/Prism.app, replacing an older copy.
install: app
	rm -rf "$(HOME)/Applications/Prism.app"
	mkdir -p "$(HOME)/Applications"
	cp -R $(APP) "$(HOME)/Applications/Prism.app"
	@echo "installed $(HOME)/Applications/Prism.app"

run: app
	open $(APP)

# Runs the app on the fixture, actions applied in memory; no tracks serve needed.
demo: app
	open -n $(APP) --args --fixture "$(abspath $(FIXTURE))"

# Captures the window from the fixture in light and dark into docs/, in the background (see scripts/shots.sh).
shots: app
	scripts/shots.sh "$(abspath $(FIXTURE))" docs

# The README's window pictures, from the made-up fixture in docs/readme, never from real tracks.
readme-shots: app
	ONLY=readme scripts/shots.sh "$(abspath ../docs/readme/fixture.json)" ../docs/readme

# Plays SCRIPT on the fixture while recording the window in the background, then prints one line per step (duration,
# late frames, duplicates) and fails when one hitches, plays twice or doubles. REDUCE_MOTION=1 runs it with Reduce Motion
# on for Prism alone and also fails on movement beyond fades. See docs/motion.md.
# Messages, cards and sessions arriving and leaving (EMI-460), on a fixture long enough to scroll.
motion-arrive:
	$(MAKE) motion FIXTURE=../mock/serve-fixture-long.json SCRIPT=scripts/motion-arrive.json

# Loading, answering, undoing and sending a decision in the composer (EMI-461).
motion-composer:
	$(MAKE) motion SCRIPT=scripts/motion-composer.json

motion: app
	swift build -c release --product prism-motion
	scripts/motion.sh "$(abspath $(FIXTURE))" "$(abspath $(SCRIPT))" "$(abspath $(MOTION_OUT))" $(if $(REDUCE_MOTION),-reduceMotion YES)

# Plays UI_SCRIPT on the fixture in light and dark with the app's state set in-process (hover, press, focus, selection),
# takes the still each step asks for and compares it with ui/baselines, one line per step. Exits 1 on a difference, with
# the diff and side-by-side images in build/ui. ui-baseline rewrites the baselines from the current build on purpose.
# See docs/motion.md.
ui-check: app
	swift build -c release --product prism-motion
	scripts/ui-check.sh check "$(abspath $(FIXTURE))" "$(abspath $(UI_SCRIPT))" "$(abspath $(UI_OUT))"

ui-baseline: app
	swift build -c release --product prism-motion
	scripts/ui-check.sh baseline "$(abspath $(FIXTURE))" "$(abspath $(UI_SCRIPT))" "$(abspath $(UI_OUT))"

clean:
	rm -rf .build build

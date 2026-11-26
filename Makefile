.PHONY: build test install install-check install-skills audit

SKILLS = track lead desk

CHECK_PLIST = dev.emilthaudal.tracks-check.plist

build:
	go build -o bin/tracks ./cmd/tracks
	go build -o bin/prism ./cmd/prism

test:
	go test ./...

# Colour captures of every pane state, for design review and before/after PR frames.
AUDIT ?= audit-out

audit:
	TRACKS_AUDIT=$(abspath $(AUDIT)) go test ./internal/tracks -run TestPaneAudit -count=1

install:
	go build -o $(HOME)/.local/bin/tracks ./cmd/tracks
	go build -o $(HOME)/.local/bin/prism ./cmd/prism

# Links each skill into Claude Code's personal skills, so /track and /lead read this checkout.
install-skills:
	mkdir -p $(HOME)/.claude/skills
	for s in $(SKILLS); do ln -sfn $(abspath skill)/$$s $(HOME)/.claude/skills/$$s; done

install-check: install
	mkdir -p $(HOME)/.local/state/tracks $(HOME)/Library/LaunchAgents
	sed 's|__HOME__|$(HOME)|g' contrib/launchd/$(CHECK_PLIST) > $(HOME)/Library/LaunchAgents/$(CHECK_PLIST)
	-launchctl bootout gui/$$(id -u)/dev.emilthaudal.tracks-check 2>/dev/null
	sleep 1
	launchctl bootstrap gui/$$(id -u) $(HOME)/Library/LaunchAgents/$(CHECK_PLIST)

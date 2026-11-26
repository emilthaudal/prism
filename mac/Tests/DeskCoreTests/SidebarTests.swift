import Foundation
import Testing
@testable import DeskCore

@Suite struct SidebarTests {
    @Test func theStructureIgnoresActivityText() throws {
        var s = try loadFixture()
        let before = Sidebar(s).structure
        let i = try #require(s.sessions.firstIndex { $0.state == .working })
        s.sessions[i].activity = "Bash: something else entirely"
        #expect(Sidebar(s).structure == before)
        #expect(Board(s).placements == Board(try loadFixture()).placements)
        s.sessions[i].status = "done"
        #expect(Board(s).placements[s.sessions[i].id] != Board(try loadFixture()).placements[s.sessions[i].id])
    }

    @Test func aTrackSitsInTheModeTheServerShowsItIn() {
        var s = Snapshot()
        s.tracks = [Track(id: "1", name: "a", mode: "background", shownMode: "waiting"), Track(id: "2", name: "b", mode: "background")]
        let sections = Sidebar(s).sections
        #expect(sections.map(\.mode) == ["background", "waiting"])
        #expect(sections[1].tracks.map(\.track.name) == ["a"])
    }

    @Test func notificationsFollowTheShownMode() {
        func style(_ mode: String, shown: String? = nil) -> NotificationStyle {
            NotificationStyle.style(for: Track(id: "1", name: "a", mode: mode, shownMode: shown))
        }
        #expect(style("focus") == .sound)
        #expect(style("background") == .silentBanner)
        #expect(style("background", shown: "waiting") == .queueOnly)
        #expect(style("parked") == .queueOnly)
        #expect(NotificationStyle.style(for: nil) == .sound)
    }

    @MainActor
    @Test func aThirdFocusTrackAsksWhichOneLeavesAndTheAnswerSwaps() async throws {
        let client = FixtureClient(snapshot: try loadFixture())
        let store = DeskStore(client: client)
        store.adopt(await client.snapshot())
        let tracks = store.snapshot.tracks
        let focus = tracks.filter { $0.mode == "focus" }
        let other = try #require(tracks.first { $0.mode != "focus" })
        #expect(focus.count >= 2)
        await store.setMode(other.id, "focus")
        let swap = try #require(store.focusSwap)
        #expect(swap.trackID == other.id)
        #expect(swap.focus.count == focus.count)
        #expect(await client.snapshot().track(other.id)?.mode == other.mode)
        let leaving = swap.focus[0]
        await store.confirmFocusSwap(leaving: leaving.name)
        #expect(store.focusSwap == nil)
        let after = await client.snapshot()
        #expect(after.track(other.id)?.mode == "focus")
        #expect(after.track(leaving.id)?.mode == "background")
    }

    @Test func aTrackListsItsSessionsOnlyWhenTwoOrMoreRun() throws {
        // Compact (Emil, 26 Nov): one row per track; the tree shows only when the track fans out.
        let sidebar = Sidebar(try loadFixture())
        let focus = sidebar.sections[0].tracks
        #expect(focus[0].nested.isEmpty)
        #expect(focus[0].line != nil)
        #expect(focus[1].nested.count == 3)
        #expect(focus[1].nested.map(\.owner) == ["desk", "desk", "desk"])
        #expect(focus[1].line == nil)
        let laBrief = try #require(sidebar.sections[1].tracks.first)
        #expect(laBrief.nested.isEmpty)
    }

    @Test func theTrackLineSaysWhatItDoesWaitsOnOrLastSaid() {
        let t = Track(id: "1", name: "pan-402", mode: "focus")
        let working = Session(id: "w", track: "1", status: "working", activity: "Bash: cd mac && swift test")
        #expect(Sidebar.row(t, sessions: [working], questions: [], all: []).line == "Running swift test")
        var waiting = t
        waiting.waitingOn = "Alex"
        let stopped = Session(id: "s", track: "1", status: "stopped", lastAnswer: "All checks are green.\nMore.")
        #expect(Sidebar.row(waiting, sessions: [stopped], questions: [], all: []).line == "Waits on Alex")
        #expect(Sidebar.row(t, sessions: [stopped], questions: [], all: []).line == "All checks are green.")
        #expect(Sidebar.row(t, sessions: [], questions: [], all: []).line == nil)
        var checked = t
        checked.check = TrackCheck(text: "pr 12 approved", answer: "no")
        #expect(Sidebar.row(checked, sessions: [stopped], questions: [], all: []).line == "Picks up when PR 12 is approved")
        checked.check = TrackCheck(text: "pr 12 approved")
        #expect(Sidebar.row(checked, sessions: [], questions: [], all: []).line == "Picks up when PR 12 is approved")
        // A check text the line doesn't know keeps the raw form.
        checked.check = TrackCheck(text: "tests pass")
        #expect(Sidebar.row(checked, sessions: [], questions: [], all: []).line == "Check tests pass: not checked yet")
    }

    @Test func aWaitingTrackNamesItsBlockerShort() {
        var t = Track(id: "1", name: "x", mode: "background", shownMode: "waiting")
        t.waitingOn = "Waits on #681 and routines #1 review, then the long note continues"
        t.check = TrackCheck(text: "pr 191 approved", answer: "no")
        let stopped = Session(id: "s", track: "1", status: "stopped")
        let row = Sidebar.row(t, sessions: [stopped], questions: [], all: [])
        #expect(row.line == "PR #191 review")
        if case .waitingOn(let who, _) = row.chip { #expect(who == "PR #191 review") } else { Issue.record("not a waiting chip") }
        // No known check: the note as written.
        t.check = nil
        #expect(Sidebar.row(t, sessions: [stopped], questions: [], all: []).line == "Waits on " + t.waitingOn!)
    }

    @Test func aTrackDecodesItsCheck() throws {
        let json = #"{"id":"1","name":"x","mode":"focus","check":{"text":"pr 12 approved","answer":"yes","ran_at":"2026-11-26T09:00:00Z"}}"#
        let t = try DeskJSON.decoder().decode(Track.self, from: Data(json.utf8))
        #expect(t.check?.text == "pr 12 approved")
        #expect(t.check?.answer == "yes")
        #expect(t.check?.ranAt != nil)
    }

    @Test func aWorkingSessionShowsItsActivityAndItsSubagents() throws {
        let sidebar = Sidebar(try loadFixture())
        let platform = try #require(sidebar.sections[0].tracks[1].nested.first { $0.session.name == "pantry-api" })
        #expect(platform.chip == .working)
        #expect(platform.activity == "Running bun test")
        #expect(platform.subagents == [Subagent(name: "PHP 8.5 delta rerun", status: "working"), Subagent(name: "lint sweep", status: "done")])
        #expect(platform.subagents.map(\.isWorking) == [true, false])
    }

    @Test func aSessionRowDoesNotRepeatItsTracksName() {
        func row(_ name: String, activity: String? = nil) -> SidebarSession {
            SidebarSession(session: Session(id: "a", name: name), chip: .idle, activity: activity, subagents: [], owner: "desk")
        }
        #expect(row("emi-400-bg-session-answers").title(under: "emi-400-bg-session-answers") == "Session")
        let working = row("recipe-search", activity: "Edit: refunds.go")
        #expect(working.title(under: "recipe-search") == "Edit: refunds.go")
        #expect(working.activityLine(under: "recipe-search") == nil)
        #expect(row("prism-redesign-restart").title(under: "prism-redesign") == "restart")
        #expect(row("pantry-api", activity: "Bash: ls").title(under: "bun-rollout") == "pantry-api")
        #expect(row("pantry-api", activity: "Bash: ls").activityLine(under: "bun-rollout") == "Bash: ls")
        #expect(row("scanner-skip-shop-api").title(under: nil) == "scanner-skip-shop-api")
        // A stopped session named after its track says what it last said, not "Session".
        let stopped = SidebarSession(session: Session(id: "b", name: "pan-402", status: "stopped", lastAnswer: "All checks on PR 187 are **green** at `ad96da9`.\nMore."), chip: .stopped, activity: nil, subagents: [], owner: "desk")
        #expect(stopped.title(under: "pan-402") == "All checks on PR 187 are green at ad96da9.")
    }

    @Test func onlyTheNewestStoppedSessionOfATrackIsListed() {
        let t = Track(id: "114", name: "pantry-api-arcade", mode: "background")
        let sessions = [
            Session(id: "d", track: "114", status: "working"),
            Session(id: "c", track: "114", status: "done"),
            Session(id: "b", track: "114", status: "stopped"),
            Session(id: "a", track: "114", status: "stopped"),
        ]
        #expect(Sidebar.listed(sessions).map(\.id) == ["d", "c"])
        #expect(Sidebar.row(t, sessions: sessions, questions: [], all: []).nested.map(\.id) == ["d", "c"])
        #expect(Sidebar.listed(Array(sessions.dropFirst(2))).map(\.id) == ["b"])
        let stoppedOnly = Sidebar.row(t, sessions: Array(sessions.dropFirst(2)), questions: [], all: [])
        #expect(stoppedOnly.nested.isEmpty)
        #expect(stoppedOnly.chip == .stopped)
    }

    @Test func activityShowsOnlyWhileTheSessionWorks() {
        var s = Session(id: "a", status: "working", activity: "Edit: serve.go")
        #expect(s.liveActivity == "Edit: serve.go")
        s.status = "done"
        #expect(s.liveActivity == nil)
        s.status = "working"
        s.activity = "  "
        #expect(s.liveActivity == nil)
    }

    @Test func aServerWithoutSubagentsStillDecodes() throws {
        let json = #"{"id": "a", "owner": "desk", "status": "idle"}"#
        let s = try DeskJSON.decoder().decode(Session.self, from: Data(json.utf8))
        #expect(s.subagents == nil)
        #expect(Sidebar.sessionRow(s, questions: []).subagents.isEmpty)
    }

    @Test func parkedTracksStartFoldedAndTheRestOpen() throws {
        let sidebar = Sidebar(try loadFixture())
        let rows = sidebar.sections.flatMap(\.tracks)
        let parked = try #require(rows.first { $0.track.mode == "parked" })
        let focus = try #require(rows.first { $0.track.mode == "focus" })
        var folds = SidebarFolds()
        #expect(!folds.isExpanded(parked))
        #expect(folds.isExpanded(focus))
        folds.toggle(parked)
        folds.toggle(focus)
        #expect(folds.isExpanded(parked))
        #expect(!folds.isExpanded(focus))
    }

    @Test func theTrackRowCountsSessionsWhileTheyShowAndCarriesTheChipWhenFolded() throws {
        let sidebar = Sidebar(try loadFixture())
        let refunds = sidebar.sections[0].tracks[0]
        // One session, so nothing is listed under it and its Question stays on the row either way.
        #expect(refunds.chip == .question)
        #expect(refunds.chip(expanded: true) == .question)
        #expect(refunds.chip(expanded: false) == .question)
        let bun = sidebar.sections[0].tracks[1]
        #expect(bun.chip(expanded: true) == .sessions(3))
        let waiting = try #require(sidebar.sections[2].tracks.first)
        #expect(waiting.chip(expanded: true) == waiting.chip)
    }
}

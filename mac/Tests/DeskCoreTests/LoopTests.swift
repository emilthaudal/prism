import Foundation
import Testing
@testable import DeskCore

@Suite struct LoopTests {
    let utc = TimeZone(identifier: "UTC")!
    let until = DeskJSON.parseDate("2026-11-27T06:00:00Z")!
    let resume = DeskJSON.parseDate("2026-11-26T16:40:00Z")!

    @Test func labelsReadInLocalTime() {
        let running = Loop(turn: 3, turns: 20, until: until)
        #expect(running.label(timeZone: utc) == "loop 3/20 · until 06:00")
        #expect(running.label(timeZone: TimeZone(identifier: "Europe/Copenhagen")!) == "loop 3/20 · until 07:00")
        #expect(running.footer(timeZone: utc) == "until 06:00")
        let paused = Loop(turn: 3, turns: 20, until: until, state: "paused", pausedUntil: resume)
        #expect(paused.label(timeZone: utc) == "loop paused until 16:40")
        #expect(paused.footer(timeZone: utc) == "paused until 16:40 · usage over budget")
        #expect(running.iteration == "iteration 3 of 20")
    }

    @Test func fractionIsClampedAndLiveMeansRunningOrPaused() {
        #expect(Loop(turn: 5, turns: 20).fraction == 0.25)
        #expect(Loop(turn: 25, turns: 20).fraction == 1)
        #expect(Loop(turn: -1, turns: 20).fraction == 0)
        #expect(Loop(turn: 1, turns: 0).fraction == 0)
        #expect(Loop(turn: 1, turns: 2).isLive)
        #expect(Loop(turn: 1, turns: 2, state: "paused").isLive)
        #expect(!Loop(turn: 1, turns: 2, state: "ended").isLive)
    }

    @Test func decodesWithAndWithoutALoop() throws {
        let with = #"{"id":"s1","owner":"desk","status":"working","loop":{"turn":3,"turns":20,"until":"2026-11-27T06:00:00Z","state":"running","paused_until":null}}"#
        let s = try DeskJSON.decoder().decode(Session.self, from: Data(with.utf8))
        #expect(s.loop == Loop(turn: 3, turns: 20, until: until))
        let without = try DeskJSON.decoder().decode(Session.self, from: Data(#"{"id":"s2","owner":"desk","status":"idle"}"#.utf8))
        #expect(without.loop == nil)
    }

    @Test func leadsTheDetailLineWithTheLabel() {
        let s = Session(id: "s1", status: "working", activity: "Bash: make bench", loop: Loop(turn: 3, turns: 20, until: until))
        #expect(s.loopLead("make bench", timeZone: utc) == "loop 3/20 · until 06:00 · make bench")
        #expect(Session(id: "s2").loopLead("make bench") == "make bench")
        #expect(Session(id: "s3", loop: Loop(turn: 1, turns: 2, state: "ended")).loopLead(nil) == nil)
    }

    @Test func stopEndsTheLoopInTheFixture() async throws {
        let snap = Snapshot(tracks: [Track(id: "125", name: "search-latency")], sessions: [Session(id: "s1", track: "125", status: "working", loop: Loop(turn: 3, turns: 20, until: until))])
        let client = FixtureClient(snapshot: snap)
        #expect(LoopBrief.all(snap).count == 1)
        try await client.perform(.stopLoop(track: "125"))
        let after = try await client.snapshot()
        #expect(after.sessions.first?.loop?.state == "ended")
        #expect(LoopBrief.all(after).isEmpty)
        #expect(DeskAction.stopLoop(track: "125").request.path == "v1/tracks/125/loop/stop")
    }
}

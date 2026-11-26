import Foundation
import Testing
@testable import DeskCore

@Suite struct AwayTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)
    private let utc = TimeZone(identifier: "UTC")!
    private func ago(_ minutes: Double) -> Date { now.addingTimeInterval(-minutes * 60) }
    private let eightHours = 8.0 * 60

    private func snapshot() -> Snapshot {
        var s = Snapshot()
        s.desk.session = "desk"
        s.tracks = [Track(id: "1", name: "pantry"), Track(id: "2", name: "recipe-search")]
        s.sessions = [
            Session(id: "desk", status: "done", updatedAt: ago(30)),
            Session(id: "a", track: "1", name: "queue-api", status: "done", lastAnswer: "**Bun** replaces node\nmore", changes: Changes(added: 14, removed: 3, merged: true), updatedAt: ago(60)),
            Session(id: "b", track: "1", name: "pantry-api", status: "done", changes: Changes(added: 5, removed: 1, merged: false), updatedAt: ago(120)),
            Session(id: "c", track: "2", name: "notes", status: "done", updatedAt: ago(180)),
            Session(id: "d", track: "2", name: "scanner", status: "error", updatedAt: ago(240)),
            Session(id: "e", track: "2", name: "old", status: "done", updatedAt: ago(eightHours + 60)),
            Session(id: "f", name: "loop", status: "idle", updatedAt: ago(90), loop: Loop(turn: 20, turns: 20, state: "ended")),
        ]
        s.handled = [
            Handled(id: "h1", at: ago(100), track: "2", kind: "nudged", did: "Reminded"),
            Handled(id: "h2", at: ago(110), track: "desk", kind: "closed", did: "Closed D4", undone: true),
            Handled(id: "h3", at: ago(120), track: "1", kind: "answered", did: "Answered D12"),
            Handled(id: "h4", at: ago(eightHours + 60), track: "1", kind: "nudged", did: "Too old"),
        ]
        s.questions = [
            Question(id: "D1", question: "Open since", createdAt: ago(50)),
            Question(id: "D2", question: "Open before", createdAt: ago(eightHours + 60)),
            Question(id: "D3", question: "Answered since", createdAt: ago(50), answer: AnswerRecord(option: 0)),
        ]
        return s
    }

    private func digest(_ s: Snapshot, away minutes: Double = 8 * 60) -> AwayDigest? {
        Away.digest(lastLooked: ago(minutes), now: now, snapshot: s, timeZone: utc)
    }

    @Test func isNilUnderTwoHoursOrWithoutALastLook() {
        let s = snapshot()
        #expect(digest(s, away: 119) == nil)
        #expect(digest(s, away: 120) != nil)
        #expect(Away.digest(lastLooked: nil, now: now, snapshot: s) == nil)
    }

    @Test func countsWhatHappenedSince() throws {
        let d = try #require(digest(snapshot()))
        #expect(d.finished == 3 && d.landed == 1 && d.ready == 1)
        #expect(d.handled == 2 && d.takenBack == 1)
        #expect(d.loopsEnded == 1 && d.decisions == 1)
        #expect(d.rows.map(\.title) == ["3 sessions finished", "Prism handled 2 things itself", "1 loop ended", "1 decision waiting"])
        #expect(d.rows[0].trailing == "1 landed · 1 ready")
        #expect(d.rows[1].trailing == "1 taken back")
    }

    @Test func leavesRowsOutAtZero() throws {
        var s = Snapshot()
        s.handled = [Handled(id: "h", at: ago(10), kind: "nudged")]
        let d = try #require(digest(s))
        #expect(d.rows.map(\.title) == ["Prism handled 1 thing itself"])
        #expect(d.rows[0].trailing == nil)
    }

    @Test func isNilWhenNothingHappened() {
        #expect(digest(Snapshot()) == nil)
    }

    @Test func listsNewestFirstWithTitlesAndDetails() throws {
        let d = try #require(digest(snapshot()))
        #expect(d.timeline.map(\.title) == [
            "queue-api landed", "recipe-search nudged", "Prism closed", "pantry-api ready to land", "notes finished", "scanner stopped with an error",
        ])
        let landed = try #require(d.timeline.first { $0.id == "s:a" })
        #expect(landed.detail == "Bun replaces node" && landed.changes?.added == 14 && landed.session == "a")
        let nudged = try #require(d.timeline.first { $0.id == "h:h1" })
        #expect(nudged.title == "recipe-search nudged" && nudged.detail == "Reminded" && nudged.track == "2")
        let desk = try #require(d.timeline.first { $0.id == "h:h2" })
        #expect(desk.title == "Prism closed" && desk.track == nil)
        #expect(d.timeline.map(\.at) == d.timeline.map(\.at).sorted(by: >))
    }

    @Test func capsTheTimelineAtEight() throws {
        var s = Snapshot()
        s.handled = (0..<11).map { Handled(id: "h\($0)", at: ago(Double(10 + $0)), kind: "nudged") }
        let d = try #require(digest(s))
        #expect(d.timeline.count == 11 && d.shown.count == 8 && d.hidden == 3)
        #expect(d.shown.first?.id == "h:h0")
        var few = Snapshot()
        few.handled = Array(s.handled.prefix(8))
        #expect(try #require(digest(few)).hidden == 0)
    }

    @Test func wordsTheHeadingAndTheTimeAway() throws {
        let d = try #require(digest(snapshot(), away: 8 * 60 + 20))
        let since = ago(8 * 60 + 20)
        #expect(d.heading == "Since " + Away.clock(since, utc))
        #expect(d.awayLine == "8 h 20 m away")
        #expect(Away.duration(2 * 3600) == "2 h")
        #expect(Away.duration(27 * 3600) == "1 d 3 h")
        #expect(Away.clock(Date(timeIntervalSince1970: 0), utc) == "00:00")
    }

    @Test func fixtureRunsNeverPersist() {
        let spy = SpyStore(stored: ago(600))
        var fixture = AwayWatch.forRun(fixture: true, awaySince: nil, store: spy)
        #expect(fixture.lastLooked == nil)
        fixture.windowBecameKey(now: now)
        fixture.windowResigned(now: now)
        fixture.dismiss(now: now)
        #expect(!fixture.showing && spy.writes.isEmpty)

        var flagged = AwayWatch.forRun(fixture: true, awaySince: ago(600), store: spy)
        flagged.windowBecameKey(now: now)
        #expect(flagged.showing)
        flagged.dismiss(now: now)
        var flaggedRun = AwayWatch.forRun(fixture: false, awaySince: ago(600), store: spy)
        flaggedRun.windowResigned(now: now)
        #expect(spy.writes.isEmpty)
    }

    @Test func aRealRunReadsAndWritesTheStore() {
        let spy = SpyStore(stored: ago(600))
        var watch = AwayWatch.forRun(fixture: false, awaySince: nil, store: spy)
        watch.windowBecameKey(now: now)
        #expect(watch.showing && watch.since == ago(600))
        watch.windowResigned(now: now.addingTimeInterval(10))
        #expect(spy.writes == [now.addingTimeInterval(10)])
    }

    @Test func theDigestLeavesAfterAMinuteInFrontAndOnGotIt() {
        var watch = AwayWatch(awaySince: ago(600))
        watch.windowBecameKey(now: now)
        watch.windowResigned(now: now.addingTimeInterval(59))
        #expect(watch.showing)
        watch.windowBecameKey(now: now.addingTimeInterval(100))
        watch.windowResigned(now: now.addingTimeInterval(160))
        #expect(!watch.showing)
        #expect(watch.lastLooked == now.addingTimeInterval(160))

        var other = AwayWatch(awaySince: ago(600))
        other.windowBecameKey(now: now)
        other.dismiss(now: now.addingTimeInterval(5))
        #expect(!other.showing && other.lastLooked == now.addingTimeInterval(5))
    }

    @Test func staysQuietUnderTwoHoursAway() {
        var watch = AwayWatch(awaySince: ago(90))
        watch.windowBecameKey(now: now)
        #expect(!watch.showing)
    }
}

private final class SpyStore: LastLookedStore, @unchecked Sendable {
    var stored: Date?
    var writes: [Date] = []
    init(stored: Date?) { self.stored = stored }
    func read() -> Date? { stored }
    func write(_ date: Date) { writes.append(date) }
}

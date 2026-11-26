import Foundation
import Testing
@testable import DeskCore

@Suite struct BoardPlanTests {
    private let now = Date()

    private func quiet() throws -> Snapshot {
        var s = try loadFixture()
        for i in s.questions.indices { s.questions[i].answer = AnswerRecord(option: 0) }
        return s
    }

    @Test func isHiddenWhileAQuestionIsOpen() throws {
        let s = try loadFixture()
        let plan = BoardPlan(s, now: now)
        #expect(!plan.isQuiet)
        #expect(plan.briefs.isEmpty && plan.waiting.isEmpty && plan.digestLine == nil)
    }

    @Test func listsFocusThenBackgroundByRankAndWaitingAfterAndNeverParked() {
        var s = Snapshot()
        s.tracks = [
            Track(id: "1", name: "bg", mode: "background", rank: 1),
            Track(id: "2", name: "f2", mode: "focus", rank: 3),
            Track(id: "3", name: "f1", mode: "focus", rank: 2),
            Track(id: "4", name: "w", mode: "waiting", rank: 0, waitingOn: "Alex"),
            Track(id: "5", name: "p", mode: "parked", rank: 0),
            Track(id: "6", name: "shownWaiting", mode: "focus", shownMode: "waiting", rank: 9),
        ]
        let plan = BoardPlan(s, now: now)
        #expect(plan.briefs.map(\.track.name) == ["f1", "f2", "bg"])
        #expect(plan.waiting.map(\.track.name) == ["w", "shownWaiting"])
        #expect(plan.waiting[0].detail == "Waits on Alex")
    }

    @Test func leavesEmptyLinesOutAndFallsBackForNow() {
        var s = Snapshot()
        s.tracks = [
            Track(id: "1", name: "a", goal: "  ", today: "", where: nil),
            Track(id: "2", name: "b", goal: "G", today: "T", where: "W"),
            Track(id: "3", name: "c"),
        ]
        s.sessions = [
            Session(id: "s3", track: "3", status: "working", activity: "Bash: swift test"),
            Session(id: "s1", track: "1", status: "done", lastAnswer: "**All** done\nmore", seenAt: Date()),
        ]
        let briefs = BoardPlan(s, now: now).briefs
        #expect(briefs[0].goal == nil && briefs[0].today == nil)
        #expect(briefs[0].now == "All done")
        #expect(briefs[1].goal == "G" && briefs[1].now == "W" && briefs[1].today == "T")
        #expect(briefs[2].now != nil && briefs[2].goal == nil)
        #expect(briefs[2].chip == .working)
    }

    @Test func linksAreTitledByHostAndLastPathPart() {
        var s = Snapshot()
        s.tracks = [Track(id: "1", name: "a", links: ["https://github.com/acme/pantry-api/pull/418", "https://example.com", "https://ci.example.com/acme/pipelines", "ftp://x.y/z", "nonsense"])]
        #expect(BoardPlan(s, now: now).briefs[0].links.map(\.title) == ["github.com #418", "example.com", "ci.example.com pipelines"])
    }

    @Test func carriesTheCheckAndThePersonWaitedOn() {
        var s = Snapshot()
        s.tracks = [Track(id: "1", name: "a", waitingOn: "Sam", check: TrackCheck(text: "pr 12 approved", answer: "no"))]
        let brief = BoardPlan(s, now: now).briefs[0]
        #expect(brief.check?.summary == "pr 12 approved: no")
        #expect(brief.person == "Sam")
    }

    @Test func digestCountsLandedHandledAndAnsweredAndOmitsZeros() throws {
        var s = try quiet()
        s.handled = []
        let landed = Board(s).landed.count
        #expect(landed > 0)
        #expect(BoardPlan(s, now: now).digest == ["\(landed) landed"])
        let day = 86400.0
        s.handled = [
            Handled(id: "a", at: now, kind: "answered"), Handled(id: "b", at: now, kind: "answered"),
            Handled(id: "c", at: now, kind: "nudged"), Handled(id: "d", at: now.addingTimeInterval(-3 * day), kind: "nudged"),
        ]
        let plan = BoardPlan(s, now: now)
        #expect(plan.digest == ["\(landed) landed", "1 handled by Prism", "2 answered"])
        #expect(plan.digestLine == "Today: \(landed) landed · 1 handled by Prism · 2 answered")
        s.sessions = []
        s.handled = []
        #expect(BoardPlan(s, now: now).digestLine == nil)
    }
}

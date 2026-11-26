import Foundation
import Testing
@testable import DeskCore

@Suite struct ReportTests {
    @Test func oneCountCoversSessionQuestionsDeskDecisionsAndBlockedSessions() {
        let snap = Snapshot(
            sessions: [Session(id: "a", status: "working"), Session(id: "b", status: "blocked"), Session(id: "c", status: "blocked")],
            questions: [
                Question(id: "q1", session: "b", from: "agent", question: "Which?"),
                Question(id: "q2", track: "t", from: "desk", question: "Merge?"),
                Question(id: "q3", from: "desk", question: "Done", answer: AnswerRecord(text: "x", by: "emil")),
            ])
        // q1 and q2 are open; c is blocked with no question of its own; b's block is its question.
        #expect(snap.waitingOnYou == 3)
        #expect(Sidebar(snap).waitingOnYou == 3)
        #expect(Board(snap).summary.hasPrefix("3 waiting on you"))
    }

    @Test func aFinishedSessionKeepsItsReportUntilSeen() {
        let changes = Changes(added: 10, removed: 2, files: 3, branch: "b1")
        var done = Session(id: "a", status: "done", lastAnswer: "Shipped it.", changes: changes)
        #expect(Board(Snapshot(sessions: [done])).reports.map(\.id) == ["a"])
        #expect(Board(Snapshot(sessions: [done])).readyToLand.isEmpty)
        done.seenAt = Date()
        let seen = Board(Snapshot(sessions: [done]))
        #expect(seen.reports.isEmpty)
        #expect(seen.readyToLand.map(\.id) == ["a"])
    }
}

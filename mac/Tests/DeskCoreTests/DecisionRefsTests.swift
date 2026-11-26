import Foundation
import Testing
@testable import DeskCore

@Suite struct DecisionRefsTests {
    @Test func findsEachDIdOnceInOrder() {
        #expect(DecisionRefs.ids(in: "Raised D41 and D7 for you; D41 is the merge.") == ["D41", "D7"])
        #expect(DecisionRefs.ids(in: "**D25, what next?** (D26)") == ["D25", "D26"])
        #expect(DecisionRefs.ids(in: "AD41 D41a D0 D 12 EMI-404 D") == [])
    }

    @Test func chipsOnlyForQuestionsTheSnapshotHolds() {
        let open = Question(id: "D41", question: "Merge?", options: [Option(label: "merge"), Option(label: "wait")], recommended: 0)
        let done = Question(id: "D42", question: "Install?", options: [Option(label: "now"), Option(label: "tonight")], recommended: 1, answer: AnswerRecord(option: 1, by: "emil"))
        let s = Snapshot(questions: [open, done])
        let qs = DecisionRefs.questions(in: "D42, D9 and D41", from: s)
        #expect(qs.map(\.id) == ["D42", "D41"])
        #expect(qs[0].chipSummary == "answered: tonight")
        #expect(qs[1].chipSummary == "1 merge ★ · 2 wait")
    }
}

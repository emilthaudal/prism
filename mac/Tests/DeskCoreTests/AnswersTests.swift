import Foundation
import Testing
@testable import DeskCore

@Suite struct AnswersTests {
    private func conversation() throws -> [ConversationEntry] {
        let url = fixtureURL.deletingLastPathComponent().appendingPathComponent("serve-fixture-answers.json")
        return try DeskJSON.decoder().decode(Snapshot.self, from: Data(contentsOf: url)).conversation
    }

    /// EMI-488's check: an answer given ten entries ago is one of the two the fixture lists, with its question.
    @Test func theFixturesDirectAnswersAreFoundWithTheirQuestions() throws {
        let c = try conversation()
        let direct = Answers.direct(c)
        #expect(direct.map(\.id) == ["m71", "m76"])
        #expect(direct.map(\.anchor) == ["m70", "m75"])
        #expect(direct[0].gist == "The jump is the new ranking step, not the index.")
        let behind = c.count - 1 - (c.firstIndex { $0.id == "m71" } ?? 0)
        #expect(behind >= 10)
        // The instruction's reply is a reply, not an answer; a message of Prism's own is neither.
        #expect(Answers.replies(c).map(\.id) == ["m67", "m71", "m76"])
    }

    @Test func steppingGoesBackFromTheBottomAndEndsThere() throws {
        let asked = Answers.asked(try conversation()).map(\.id)
        #expect(asked == ["m66", "m70", "m75"])
        #expect(Answers.step(from: nil, by: -1, in: asked) == "m75")
        #expect(Answers.step(from: "m75", by: -1, in: asked) == "m70")
        #expect(Answers.step(from: "m66", by: -1, in: asked) == "m66")
        #expect(Answers.step(from: "m75", by: 1, in: asked) == nil)
        #expect(Answers.step(from: nil, by: 1, in: asked) == nil)
    }

    @Test func keptMessagesKeepTheirOrderAndTheirQuestion() throws {
        let kept = Answers.kept(["m79", "m71"], in: try conversation())
        #expect(kept.map(\.id) == ["m79", "m71"])
        #expect(kept[0].question == nil)
        #expect(kept[1].question?.id == "m70")
    }
}

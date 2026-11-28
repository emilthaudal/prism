import Foundation
import Testing
@testable import DeskCore

@Suite struct AnswersTests {
    private func conversation() throws -> [ConversationEntry] {
        let url = fixtureURL.deletingLastPathComponent().appendingPathComponent("serve-fixture-answers.json")
        return try DeskJSON.decoder().decode(Snapshot.self, from: Data(contentsOf: url)).conversation
    }

    @Test func repliesPairEachQuestionWithPrismsFirstAnswer() throws {
        let c = try conversation()
        let replies = Answers.replies(c)
        // A message of Prism's own that answered nothing is not a reply.
        #expect(replies.map(\.id) == ["m67", "m71", "m76"])
        #expect(replies.map(\.anchor) == ["m66", "m70", "m75"])
        #expect(replies[1].gist == "The jump is the new ranking step, not the index.")
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

import Foundation
import Testing
@testable import DeskCore

@Suite struct MarkGroupsTests {
    private func conversation(_ roles: String) -> [ConversationEntry] {
        roles.enumerated().map { i, c in
            ConversationEntry(id: "m\(i)", role: c == "e" ? "emil" : "desk", text: "")
        }
    }

    @Test func marksFarEnoughApartStayOwnGroups() {
        // Ten entries over 100 points: 10 points each.
        let groups = MarkGroups.group(conversation("edddeddded"), span: 100, minGap: 10)
        #expect(groups.map(\.ids) == [["m0"], ["m4"], ["m8"]])
    }

    @Test func marksCloserThanTheGapMergeAndGoToTheNewest() {
        let groups = MarkGroups.group(conversation("eeeddddeee"), span: 100, minGap: 25)
        #expect(groups.map(\.ids) == [["m0", "m1", "m2"], ["m7", "m8", "m9"]])
        #expect(groups.map(\.target) == ["m2", "m9"])
    }

    @Test func groupsOfARealisticConversationSitAtLeastAMarkApart() {
        let c = conversation(String((0..<1_870).map { $0 % 12 == 0 ? Character("e") : Character("d") }))
        let groups = MarkGroups.group(c, span: 540, minGap: 24)
        #expect(groups.count < 25)
        #expect(groups.flatMap(\.ids).count == 156)
        for (a, b) in zip(groups, groups.dropFirst()) { #expect(b.y - a.y >= 24) }
    }

    @Test func noMessagesOfEmilsMakeNoGroups() {
        #expect(MarkGroups.group(conversation("dddd"), span: 100, minGap: 10).isEmpty)
        #expect(MarkGroups.group([], span: 100, minGap: 10).isEmpty)
    }
}

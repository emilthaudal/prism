import Foundation
import Testing
@testable import DeskCore

@Suite struct FixtureTests {
    @Test func decodesTheMockupContent() throws {
        let s = try loadFixture()
        #expect(s.seq == 1042)
        #expect(s.desk.session?.hasPrefix("c45dc3eb") == true)
        #expect(s.tracks.map(\.name).contains("invoice-export"))
        #expect(s.track("102")?.waitingOnPerson == "Alex")

        let d14 = try #require(s.question("D14"))
        #expect(d14.options.map(\.label) == ["Merge now, squash", "Wait for Alex", "Hold until tomorrow"])
        #expect(d14.recommendedIndex == 0)
        #expect(d14.evidence?.first == Evidence(text: "CI green", source: "CircleCI #48211"))
        #expect(d14.isOpen)

        let scanner = try #require(s.sessions.first { $0.name == "scanner-skips-shop-api" })
        #expect(scanner.waitsOnPermission)
        let platform = try #require(s.sessions.first { $0.name == "pantry-api" })
        #expect(platform.state == .working)
        #expect(platform.changes == Changes(added: 88, removed: 61, files: 9, branch: "bun/pantry-api", merged: false))

        #expect(s.conversation.map(\.role) == ["emil", "desk", "report", "report", "report", "desk", "desk"])
        #expect(s.conversation[5].outcome == "reasked")
        #expect(DecisionRefs.questions(in: s.conversation[6].text, from: s).map(\.id) == ["D14", "D15"])
        #expect(s.conversation[1].steps == 4)
        #expect(s.generatedAt == DeskJSON.parseDate("2026-11-24T13:32:00Z"))
    }

    @Test func boardAndSidebarMatchTheMockup() throws {
        let s = try loadFixture()
        let board = Board(s)
        #expect(board.needsYou.map(\.id) == ["D14", "D15"])
        #expect(board.inFlight.compactMap(\.name) .sorted() == ["app-template-node", "pantry-api", "scanner-skips-shop-api"])
        // A finished session Emil hasn't seen is a report first; once seen it is ready to land.
        #expect((board.reports + board.readyToLand).compactMap(\.name).sorted() == ["queue-api", "recipe-search"])
        #expect(board.reports.compactMap(\.name) == ["queue-api"])
        #expect(board.landed.count == 3)

        let sidebar = Sidebar(s)
        #expect(sidebar.sections.map(\.title) == ["Focus", "Background", "Waiting", "Parked"])
        let focus = sidebar.sections[0].tracks
        #expect(focus.map(\.track.name) == ["recipe-search", "bun-rollout", "scanner-skips-shop-api"])
        #expect(focus[0].chip == .question)
        #expect(focus[1].chip == .sessions(3))
        #expect(focus[1].nested.count == 3)
        #expect(focus[0].nested.isEmpty)
        #expect(focus[2].chip == .permission)
        let laBrief = try #require(sidebar.sections[1].tracks.first)
        #expect(laBrief.ownedByEmil)
        #expect(laBrief.chip == .done)
        #expect(sidebar.sections[2].tracks.first?.chip == .waitingOn("Alex", since: DeskJSON.parseDate("2026-11-24T10:30:00Z")))
    }

    @Test func fixtureClientAppliesActionsInMemory() async throws {
        let clock = TestClock()
        let client = FixtureClient(snapshot: try loadFixture(), clock: { clock.now })
        try await client.perform(.answer(question: "D14", .option(0)))
        var s = await client.snapshot()
        #expect(s.seq == 1043)
        #expect(s.question("D14")?.answer?.option == 0)
        await #expect(throws: ClientError.http(status: 409, message: "D14 is already answered")) {
            try await client.perform(.answer(question: "D14", .option(1)))
        }
        let scanner = "5d6e7f80-9a0b-4c1d-8e2f-4a5b6c7d8e94"
        let reply = try await client.perform(.takeover(session: scanner))
        #expect(reply.command == "claude attach 5d6e7f80")
        s = await client.snapshot()
        #expect(s.session(scanner)?.owner == "emil")
        try await client.perform(.giveback(session: scanner))
        s = await client.snapshot()
        #expect(s.session(scanner)?.owner == "desk")
    }
}

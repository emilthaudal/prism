import Foundation
import Testing
@testable import DeskCore

private let d14 = Question(id: "D14", question: "Merge?", options: [Option(label: "Merge now"), Option(label: "Wait"), Option(label: "Hold")], recommended: 0)
private let d15 = Question(id: "D15", question: "Allow?", options: [Option(label: "Allow once"), Option(label: "Deny")], recommended: 0)

@Suite struct AnswerQueueTests {
    @Test func waitsFiveSecondsBeforeAnAnswerIsDue() {
        let t0 = Date(timeIntervalSince1970: 0)
        var q = AnswerQueue()
        let p = q.start(d14, choice: .option(1), now: t0)
        #expect(p.label == "Wait")
        #expect(p.secondsLeft(at: t0) == 5)
        #expect(p.secondsLeft(at: t0.addingTimeInterval(0.5)) == 5)
        #expect(p.secondsLeft(at: t0.addingTimeInterval(4.2)) == 1)
        #expect(q.takeDue(now: t0.addingTimeInterval(4.99)).isEmpty)
        #expect(q.takeDue(now: t0.addingTimeInterval(5)) == [p])
        #expect(q.isEmpty)
    }

    @Test func marksAnAnswerThatIsNotTheRecommendation() {
        let t0 = Date(timeIntervalSince1970: 0)
        var q = AnswerQueue()
        #expect(q.start(d14, choice: .option(0), now: t0).againstRecommendation == false)
        #expect(q.start(d14, choice: .option(1), now: t0).againstRecommendation)
        #expect(q.start(d14, choice: .text("later"), now: t0).againstRecommendation)
    }

    @Test func undoTakesBackTheNewestOrANamedAnswer() {
        let t0 = Date(timeIntervalSince1970: 0)
        var q = AnswerQueue()
        q.start(d14, choice: .option(0), now: t0)
        q.start(d15, choice: .text("only for drafts"), now: t0.addingTimeInterval(1))
        #expect(q.undo()?.question == "D15")
        #expect(q.undo("D14")?.question == "D14")
        #expect(q.undo() == nil)
        #expect(q.takeDue(now: t0.addingTimeInterval(60)).isEmpty)
    }

    @Test func aSecondAnswerRestartsTheCountdown() {
        let t0 = Date(timeIntervalSince1970: 0)
        var q = AnswerQueue()
        q.start(d14, choice: .option(0), now: t0)
        q.start(d14, choice: .option(2), now: t0.addingTimeInterval(3))
        #expect(q.items.count == 1)
        #expect(q.takeDue(now: t0.addingTimeInterval(5)).isEmpty)
        #expect(q.takeDue(now: t0.addingTimeInterval(8)).map(\.choice) == [.option(2)])
    }
}

@MainActor
@Suite struct StoreAnswerTests {
    private func store(_ clock: TestClock) async throws -> (DeskStore, ScriptedClient) {
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1, questions: [d14, d15])], batches: [])
        let store = DeskStore(client: client, clock: { clock.now })
        _ = try await store.syncOnce()
        return (store, client)
    }

    @Test func sendsOnlyAfterTheCountdown() async throws {
        let clock = TestClock()
        let (store, client) = try await store(clock)
        store.answerRecommended("D14")
        await store.tick()
        clock.advance(4)
        await store.tick()
        #expect(await client.actions.isEmpty)
        #expect(store.answers.pending(for: "D14")?.secondsLeft(at: clock.now) == 1)
        clock.advance(1)
        await store.tick()
        #expect(await client.actions == [.answer(question: "D14", .option(0))])
        #expect(store.answers.isEmpty)
    }

    @Test func theCardStaysGoneOnceItsAnswerIsSent() async throws {
        let clock = TestClock()
        let (store, _) = try await store(clock)
        store.answerRecommended("D14")
        clock.advance(5)
        await store.tick()
        #expect(store.snapshot.question("D14")?.isOpen == false)
        #expect(store.snapshot.openQuestions.map(\.id) == ["D15"])
    }

    @Test func theCardComesBackWhenItsAnswerDidNotGo() async throws {
        let clock = TestClock()
        let (store, client) = try await store(clock)
        await client.setFailure(.http(status: 500, message: "boom"))
        store.answerRecommended("D14")
        clock.advance(5)
        await store.tick()
        #expect(store.snapshot.question("D14")?.isOpen == true)
    }

    @Test func enterOnAQuestionWithNoOptionsTakesTheRecommendationInWords() async throws {
        let clock = TestClock()
        let open = Question(id: "D20", question: "Keep the fill colour?", recommendation: "yes, the contrast holds")
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1, questions: [open, Question(id: "D21", question: "Plain?")])], batches: [])
        let store = DeskStore(client: client, clock: { clock.now })
        _ = try await store.syncOnce()
        #expect(open.recommendationText == "yes, the contrast holds")
        #expect(store.snapshot.question("D21")?.recommendedChoice == nil)
        store.answerRecommended("D21")
        #expect(store.answers.isEmpty)
        store.answerRecommended("D20")
        #expect(store.answers.pending(for: "D20")?.secondsLeft(at: clock.now) == 5)
        clock.advance(5)
        await store.tick()
        #expect(await client.actions == [.answer(question: "D20", .text("Go with your recommendation: yes, the contrast holds"))])
    }

    @Test func enterPrefersTheRecommendedOptionOverWords() {
        let q = Question(id: "D1", question: "?", options: [Option(label: "Yes"), Option(label: "No")], recommended: 1, recommendation: "no, too risky")
        #expect(q.recommendedChoice == .option(1))
        #expect(q.recommendationText == nil)
    }

    @Test func undoInTimeSendsNothing() async throws {
        let clock = TestClock()
        let (store, client) = try await store(clock)
        store.answer("D15", choice: .option(1))
        clock.advance(4.9)
        #expect(store.undo())
        clock.advance(10)
        await store.tick()
        #expect(await client.actions.isEmpty)
        #expect(!store.undo())
    }

    @Test func ignoresAnswersThatCannotBeSent() async throws {
        let clock = TestClock()
        let (store, _) = try await store(clock)
        store.answer("D14", choice: .option(7))
        store.answer("D14", choice: .text("   "))
        store.answer("D99", choice: .option(0))
        #expect(store.answers.isEmpty)
    }

    @Test func aNotificationAnswerSkipsTheCountdown() async throws {
        let clock = TestClock()
        let (store, client) = try await store(clock)
        store.answer("D14", choice: .option(2))
        await store.answerNow("D14", choice: .option(0))
        #expect(await client.actions == [.answer(question: "D14", .option(0))])
        #expect(store.answers.isEmpty)
    }

    @Test func aFailedSendShowsTheError() async throws {
        let clock = TestClock()
        let (store, client) = try await store(clock)
        await client.setFailure(.http(status: 409, message: "D14 is already answered"))
        await store.answerNow("D14", choice: .option(0))
        #expect(store.notice == Notice(text: "D14 is already answered", isError: true))
    }

    @Test func anAnswerQueuedForABusyAgentIsAQuietNoticeNotAnError() async throws {
        let clock = TestClock()
        let asked = Question(id: "D9", track: "7", from: "agent", question: "Which index?", options: [Option(label: "products")], recommended: 0)
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1, tracks: [Track(id: "7", name: "acme-legacy-uplift")], questions: [asked])], batches: [])
        await client.setReply(ActionReply(delivery: "queued", status: "queued D9"))
        let store = DeskStore(client: client, clock: { clock.now })
        _ = try await store.syncOnce()
        await store.answerNow("D9", choice: .option(0))
        #expect(store.notice == Notice(text: "Queued for acme-legacy-uplift: sent when its agent stops", symbol: "clock"))
        #expect(store.notice?.isError == false)
    }

    @Test func aDeliveredAnswerLeavesNoNotice() async throws {
        let clock = TestClock()
        let (store, _) = try await store(clock)
        await store.answerNow("D14", choice: .option(0))
        #expect(store.notice == nil)
    }

    @Test func takeOverWithoutRexShowsTheCommand() async throws {
        let clock = TestClock()
        let (store, _) = try await store(clock)
        await store.takeOver("abc-session")
        #expect(store.notice?.command == "claude attach abc")
    }
}

@Suite struct QueuedBoardTests {
    @Test func aQueuedAnswerLeavesNeedsYouForItsOwnGroup() throws {
        let at = AnswerRecord(option: 0, by: "emil", at: Date(timeIntervalSince1970: 5))
        let queued = Question(id: "D9", track: "7", from: "agent", question: "Which index?", options: [Option(label: "products")], answer: at, delivery: "queued")
        let delivered = Question(id: "D8", track: "7", from: "agent", question: "Merge?", options: [Option(label: "now")], answer: at, delivery: "delivered")
        let json = try DeskJSON.decoder().decode(Question.self, from: Data(#"{"id":"D7","question":"x","options":[],"answer":{"option":0,"by":"emil"},"delivery":"queued"}"#.utf8))
        #expect(json.isQueued)
        let board = Board(Snapshot(seq: 1, questions: [queued, delivered, d14]))
        #expect(board.queued.map(\.id) == ["D9"])
        #expect(board.needsYou.map(\.id) == ["D14"])
    }
}

@Suite struct KeyTests {
    @Test func bareKeysActOutsideTheComposer() {
        #expect(Keys.command(for: KeyPress(characters: "\r", isReturn: true), editing: false) == .takeRecommendation)
        #expect(Keys.command(for: KeyPress(characters: "3"), editing: false) == .pick(2))
        #expect(Keys.command(for: KeyPress(characters: "0"), editing: false) == nil)
        #expect(Keys.command(for: KeyPress(characters: "z"), editing: false) == .undo)
        #expect(Keys.command(for: KeyPress(characters: "t"), editing: false) == .takeOver)
        #expect(Keys.command(for: KeyPress(characters: "g"), editing: false) == .giveBack)
    }

    @Test func typingInTheComposerStaysTyping() {
        #expect(Keys.command(for: KeyPress(characters: "3"), editing: true) == nil)
        #expect(Keys.command(for: KeyPress(characters: "t"), editing: true) == nil)
        #expect(Keys.command(for: KeyPress(characters: "\r", isReturn: true), editing: true) == nil)
        #expect(Keys.command(for: KeyPress(characters: "\r", isReturn: true), editing: true) == nil)
        #expect(Keys.command(for: KeyPress(characters: "2", control: true), editing: true) == .pick(1))
        #expect(Keys.command(for: KeyPress(characters: "z", command: true), editing: true) == .undo)
        #expect(Keys.command(for: KeyPress(characters: "z", command: true, option: true), editing: false) == nil)
    }
}

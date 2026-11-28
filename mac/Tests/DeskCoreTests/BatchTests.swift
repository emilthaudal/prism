import Foundation
import Testing
@testable import DeskCore

private func asked(_ id: String, track: String = "7", session: String = "s7", options: [String] = ["yes", "no"], recommended: Int? = 0) -> Question {
    Question(id: id, track: track, session: session, from: "agent", question: "Ask \(id)?", options: options.map { Option(label: $0) }, recommended: recommended)
}

@MainActor
@Suite struct BatchTests {
    private func store(_ questions: [Question], sessions: [Session] = [], clock: TestClock) async throws -> (DeskStore, ScriptedClient) {
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1, tracks: [Track(id: "7", name: "refunds"), Track(id: "8", name: "labels")], sessions: sessions, questions: questions)], batches: [])
        let store = DeskStore(client: client, clock: { clock.now })
        _ = try await store.syncOnce()
        return (store, client)
    }

    @Test func anAskerWithSeveralQuestionsStagesAnswersAndSendsOneCallAfterTheLast() async throws {
        let clock = TestClock()
        let (store, client) = try await store([asked("D1"), asked("D2"), asked("D3")], clock: clock)
        store.answer("D1", choice: .option(1))
        store.answer("D2", choice: .text("only for drafts"))
        #expect(store.answers.isEmpty)
        #expect(store.stagedChoice(for: "D1") == .option(1))
        #expect(store.stagedCount(for: "track 7") == 2)
        clock.advance(60)
        await store.tick()
        #expect(await client.actions.isEmpty)

        store.answer("D3", choice: .option(0))
        #expect(store.staged.isEmpty)
        #expect(store.answers.batch(containing: "D2")?.count == 3)
        clock.advance(4)
        await store.tick()
        #expect(await client.actions.isEmpty)
        clock.advance(1)
        await store.tick()
        #expect(await client.actions == [.answers([
            BatchItem(question: "D1", choice: .option(1)),
            BatchItem(question: "D2", choice: .text("only for drafts")),
            BatchItem(question: "D3", choice: .option(0)),
        ])])
        #expect(store.answers.isEmpty)
    }

    @Test func aStagedAnswerCanBeChangedAndTakenBack() async throws {
        let clock = TestClock()
        let (store, _) = try await store([asked("D1"), asked("D2")], clock: clock)
        store.answer("D1", choice: .option(0))
        store.answer("D1", choice: .option(1))
        #expect(store.stagedChoice(for: "D1") == .option(1))
        store.unstage("D1")
        #expect(store.stagedChoice(for: "D1") == nil)
        #expect(store.stagedCount(for: "track 7") == 0)
    }

    @Test func sendStagedSendsWhatIsStagedThoughAQuestionIsStillOpen() async throws {
        let clock = TestClock()
        let (store, client) = try await store([asked("D1"), asked("D2"), asked("D3")], clock: clock)
        store.answer("D1", choice: .option(0))
        store.answer("D3", choice: .option(1))
        #expect(store.stagedDialog(preferring: "D2") == "track 7")
        store.sendStaged(dialog: "track 7")
        clock.advance(5)
        await store.tick()
        #expect(await client.actions == [.answers([BatchItem(question: "D1", choice: .option(0)), BatchItem(question: "D3", choice: .option(1))])])
    }

    @Test func undoTakesTheWholeBatchBackToStaged() async throws {
        let clock = TestClock()
        let (store, client) = try await store([asked("D1"), asked("D2")], clock: clock)
        store.answer("D1", choice: .option(0))
        store.answer("D2", choice: .option(1))
        #expect(store.undo("D2"))
        #expect(store.answers.isEmpty)
        #expect(store.stagedChoice(for: "D1") == .option(0) && store.stagedChoice(for: "D2") == .option(1))
        clock.advance(60)
        await store.tick()
        #expect(await client.actions.isEmpty)
    }

    @Test func oneOpenQuestionSendsAsItAlwaysDid() async throws {
        let clock = TestClock()
        let (store, client) = try await store([asked("D1"), asked("D2", track: "8", session: "s8")], clock: clock)
        store.answer("D1", choice: .option(0))
        store.answer("D2", choice: .option(1))
        #expect(store.staged.isEmpty)
        clock.advance(5)
        await store.tick()
        #expect(await client.actions == [.answer(question: "D1", .option(0)), .answer(question: "D2", .option(1))])
    }

    @Test func theDesksOwnQuestionsAreAnsweredOneByOne() async throws {
        let clock = TestClock()
        let (store, client) = try await store([Question(id: "D1", question: "a?", options: [Option(label: "x")]), Question(id: "D2", question: "b?", options: [Option(label: "y")])], clock: clock)
        store.answer("D1", choice: .option(0))
        #expect(store.staged.isEmpty)
        clock.advance(5)
        await store.tick()
        #expect(await client.actions == [.answer(question: "D1", .option(0))])
    }

    @Test func aPermissionPromptIsNeverStaged() async throws {
        let clock = TestClock()
        let blocked = Session(id: "s7", track: "7", status: "blocked", waitingFor: "permission to run gh")
        let (store, client) = try await store([asked("D1"), asked("D2")], sessions: [blocked], clock: clock)
        store.answer("D1", choice: .option(0))
        #expect(store.staged.isEmpty)
        clock.advance(5)
        await store.tick()
        #expect(await client.actions == [.answer(question: "D1", .option(0))])
        #expect(!store.canAsk(asked("D2")))
    }

    @Test func aBatchQueuedForABusyAgentIsAQuietNotice() async throws {
        let clock = TestClock()
        let (store, client) = try await store([asked("D1"), asked("D2")], clock: clock)
        await client.setReply(ActionReply(delivery: "queued", status: "queued D1, D2"))
        store.answer("D1", choice: .option(0))
        store.answer("D2", choice: .option(0))
        clock.advance(5)
        await store.tick()
        #expect(store.notice == Notice(text: "Queued for refunds: sent when its agent stops", symbol: "clock"))
    }

    @Test func aTracksAsksAndTheDesksDecisionsOnItAreOneDialogInMergeInstallCloseOrder() async throws {
        let clock = TestClock()
        let close = Question(id: "D1", track: "7", session: "s7", from: "agent", question: "Close this track?", options: [Option(label: "yes")], recommended: 0, createdAt: Date(timeIntervalSince1970: 1))
        let install = Question(id: "D2", track: "7", from: "desk", question: "Install the sidebar usage bars?", options: [Option(label: "install"), Option(label: "wait")], recommended: 0, createdAt: Date(timeIntervalSince1970: 2))
        let merge = Question(id: "D3", track: "7", session: "s7", from: "agent", question: "Merge #212 now?", options: [Option(label: "merge")], recommended: 0, createdAt: Date(timeIntervalSince1970: 3))
        let other = asked("D4", track: "8", session: "s8")
        let (store, client) = try await store([close, install, merge, other], clock: clock)
        #expect(store.dialog(of: install).map(\.id) == ["D3", "D2", "D1"])
        // D4 has no time, so it counts as oldest; track 7's three sit together after it.
        #expect(Board(store.snapshot).needsYou.map(\.id) == ["D4", "D3", "D2", "D1"])
        #expect(store.nextUndecided(from: close).id == "D1")

        store.answer("D3", choice: .option(0))
        #expect(store.nextUndecided(from: merge).id == "D2")
        store.answer("D2", choice: .option(1))
        #expect(store.answers.isEmpty)
        store.answer("D1", choice: .option(0))
        #expect(store.answers.batch(containing: "D2")?.count == 3)
        clock.advance(5)
        await store.tick()
        // The agent gets its two answers as one message; the desk its one.
        #expect(await client.actions == [
            .answers([BatchItem(question: "D3", choice: .option(0)), BatchItem(question: "D1", choice: .option(0))]),
            .answer(question: "D2", .option(1)),
        ])
    }

    @Test func aBatchIsOneRequest() {
        let request = DeskAction.answers([BatchItem(question: "D1", choice: .option(1)), BatchItem(question: "D2", choice: .text("x"))]).request
        #expect(request.path == "v1/answers")
        let answers = request.body?["answers"] as? [[String: any Sendable]]
        #expect(answers?.count == 2)
        #expect(answers?[0]["id"] as? String == "D1" && answers?[0]["option"] as? Int == 1)
        #expect(answers?[1]["id"] as? String == "D2" && answers?[1]["text"] as? String == "x")
    }

    @Test func fixtureAppliesABatchAsOneCall() async throws {
        let clock = TestClock()
        let client = FixtureClient(snapshot: Snapshot(seq: 1, questions: [asked("D1"), asked("D2")]), clock: { clock.now })
        let reply = try await client.perform(.answers([BatchItem(question: "D1", choice: .option(0)), BatchItem(question: "D2", choice: .option(1))]))
        #expect(reply.status == "answered D1, D2")
        let s = await client.snapshot()
        #expect(s.seq == 3 && s.openQuestions.isEmpty)
    }
}

@MainActor
@Suite struct AskForMoreTests {
    @Test func askingSendsTheTextAndLeavesTheQuestionOpen() async throws {
        let clock = TestClock()
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1, tracks: [Track(id: "7", name: "refunds")], questions: [asked("D1"), asked("D2")])], batches: [])
        let store = DeskStore(client: client, clock: { clock.now })
        _ = try await store.syncOnce()
        await store.ask("D1", "  which is faster?  ")
        #expect(await client.actions == [.ask(question: "D1", text: "which is faster?")])
        #expect(store.snapshot.question("D1")?.isOpen == true)
        #expect(store.answers.isEmpty && store.staged.isEmpty)
        #expect(store.notice == Notice(text: "Asked refunds about D1", symbol: "questionmark.bubble", questionID: "D1"))
        await store.ask("D1", "   ")
        #expect(await client.actions.count == 1)
    }

    @Test func showMeTheOptionsIsOfferedOnlyForOptionsWithoutPictures() async throws {
        #expect(asked("D1").canShowOptions)
        #expect(!Question(id: "D2", question: "Free text?").canShowOptions)
        let pictured = Question(id: "D3", question: "Icon?", options: [Option(label: "owl", attachment: Attachment(label: "owl.png", url: "file:///tmp/owl.png", kind: "image"))])
        #expect(!pictured.canShowOptions)
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1, questions: [asked("D1")])], batches: [])
        let store = DeskStore(client: client)
        _ = try await store.syncOnce()
        await store.showOptions("D1")
        #expect(await client.actions == [.showOptions(question: "D1")])
        #expect(DeskAction.showOptions(question: "D1").request.path == "v1/questions/D1/ask")
        #expect(DeskAction.ask(question: "D1", text: "why").request.body?["text"] as? String == "why")
    }

    @Test func fixtureAskDoesNotAnswer() async throws {
        let client = FixtureClient(snapshot: Snapshot(seq: 1, questions: [asked("D1")]))
        let reply = try await client.perform(.ask(question: "D1", text: "why"))
        #expect(reply.status == "asked about D1")
        #expect(await client.snapshot().question("D1")?.isOpen == true)
    }
}

@Suite struct ComposerFocusTests {
    @Test func theDeskFieldIsOneKeystrokeAway() {
        let cmdL = KeyPress(characters: "l", command: true)
        #expect(Keys.command(for: cmdL, editing: false) == .focusDesk)
        #expect(Keys.command(for: cmdL, editing: true) == .focusDesk)
        #expect(ComposerFocus.target(of: .focusDesk, hasQuestion: false) == .desk)
        #expect(ComposerFocus.target(of: .focusDesk, hasQuestion: true) == .desk)
    }

    @Test func digitsAndReturnActOnTheQuestionOnlyWithNoTextFieldFocused() {
        let one = KeyPress(characters: "1"), enter = KeyPress(characters: "\r", isReturn: true)
        #expect(Keys.command(for: one, editing: false) == .pick(0))
        #expect(Keys.command(for: enter, editing: false) == .takeRecommendation)
        #expect(Keys.command(for: one, editing: true) == nil)
        #expect(Keys.command(for: enter, editing: true) == nil)
    }

    @Test func mAsksForMoreOnlyOutsideAFieldAndNeedsAQuestion() {
        let m = KeyPress(characters: "m")
        #expect(Keys.command(for: m, editing: false) == .askMore)
        #expect(Keys.command(for: m, editing: true) == nil)
        #expect(ComposerFocus.target(of: .askMore, hasQuestion: true) == .ask)
        #expect(ComposerFocus.target(of: .askMore, hasQuestion: false) == nil)
    }

    @Test func commandReturnSendsStagedAnswers() {
        #expect(Keys.command(for: KeyPress(characters: "\r", command: true, isReturn: true), editing: true) == .sendStaged)
        #expect(Keys.command(for: KeyPress(characters: "\r", command: true, isReturn: true), editing: false) == .sendStaged)
    }

    @Test func aQuestionWithNothingToPickFocusesItsAnswerFieldElseTheCursorStays() {
        #expect(ComposerFocus.initial(for: Question(id: "D1", question: "Why?")) == .answer)
        #expect(ComposerFocus.initial(for: Question(id: "D1", question: "Which?", options: [Option(label: "a")])) == nil)
        #expect(ComposerFocus.initial(for: Question(id: "D1", question: "Go?", recommendation: "yes, go")) == nil)
        #expect(ComposerFocus.initial(for: nil) == nil)
    }
}

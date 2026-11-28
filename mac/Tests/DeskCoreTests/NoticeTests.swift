import Foundation
import Testing
@testable import DeskCore

@MainActor
@Suite struct NoticeTests {
    private let asked = Question(id: "D9", track: "7", from: "agent", question: "Which index?", options: [Option(label: "products")], recommended: 0)
    private let track = Track(id: "7", name: "acme-legacy-uplift")

    @Test func askingAboutAQuestionSaysSoUntilThatQuestionCloses() async throws {
        var answered = asked
        answered.answer = AnswerRecord(option: 0, by: "emil", at: Date())
        let client = ScriptedClient(snapshots: [
            Snapshot(seq: 1, tracks: [track], questions: [asked]),
            Snapshot(seq: 2, tracks: [track], questions: [answered]),
        ], batches: [])
        let store = DeskStore(client: client)
        store.noticeLifetime = nil
        _ = try await store.syncOnce()
        await store.ask("D9", "Which one is faster?")
        #expect(store.notice == Notice(text: "Asked acme-legacy-uplift about D9", symbol: "questionmark.bubble", questionID: "D9"))

        _ = try await store.syncOnce()
        #expect(store.notice == nil)
    }

    @Test func aQuietNoticeLeavesOnItsOwnAndAnErrorStays() async throws {
        let slept = NoticeSleeps()
        let store = DeskStore(client: FixtureClient(snapshot: Snapshot(seq: 1)), sleep: { d in await slept.add(d) })
        store.notice = Notice(text: "Asked acme-legacy-uplift about D9", symbol: "questionmark.bubble")
        await store.noticeExpiry?.value
        #expect(store.notice == nil)
        #expect(await slept.durations == [.seconds(5)])

        store.notice = Notice(text: "D14 is already answered", isError: true)
        #expect(store.noticeExpiry == nil)
        store.notice = Notice(text: "Rex isn't running. Open it in a terminal:", command: "rex")
        #expect(store.noticeExpiry == nil)
        #expect(store.notice?.command == "rex")
    }

    @Test func aNewNoticeIsNotClearedByTheOneBefore() async throws {
        let gate = Gate()
        let store = DeskStore(client: FixtureClient(snapshot: Snapshot(seq: 1)), sleep: { _ in await gate.wait() })
        store.notice = Notice(text: "first", symbol: "clock")
        let first = store.noticeExpiry
        store.notice = Notice(text: "second", isError: true)
        await gate.open()
        await first?.value
        #expect(store.notice?.text == "second")
    }
}

private actor NoticeSleeps {
    var durations: [Duration] = []
    func add(_ d: Duration) { durations.append(d) }
}

/// Holds every sleeper until opened.
private actor Gate {
    private var isOpen = false
    private var waiting: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        if isOpen { return }
        await withCheckedContinuation { waiting.append($0) }
    }

    func open() {
        isOpen = true
        waiting.forEach { $0.resume() }
        waiting = []
    }
}

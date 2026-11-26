import Foundation
import Testing
@testable import DeskCore

private func trackEvent(_ seq: Int, name: String) -> StreamEvent {
    StreamEvent(seq: seq, change: .track(Track(id: "t\(seq)", name: name)))
}

@Suite struct SSETests {
    @Test func parsesMessagesAcrossChunksAndSkipsComments() throws {
        let raw = ": keep-alive\r\nid: 7\r\ndata: {\"seq\": 7, \"op\": \"remove\", \"kind\": \"question\", \"value\": {\"id\": \"D1\"}}\r\n\r\n: ping\n\nid: 8\ndata: {\"seq\": 8,\ndata: \"op\": \"upsert\", \"kind\": \"desk\", \"value\": {\"status\": \"working\"}}\n\n"
        var lines = LineSplitter()
        var parser = SSEParser()
        var messages: [SSEMessage] = []
        for byte in Array(raw.utf8) {
            if let line = lines.feed(byte), let m = parser.feed(line: line) { messages.append(m) }
        }
        #expect(messages.map(\.id) == ["7", "8"])
        let events = try messages.map { try event($0.data) }
        #expect(events[0] == StreamEvent(seq: 7, change: .remove(kind: "question", id: "D1")))
        #expect(events[1] == StreamEvent(seq: 8, change: .desk(DeskInfo(status: "working"))))
    }
}

@Suite struct ActionRequestTests {
    @Test func actionsMapToTheContractsRoutes() {
        #expect(DeskAction.answer(question: "D14", .option(0)).request.path == "v1/questions/D14/answer")
        #expect(DeskAction.answer(question: "D14", .option(2)).request.body?["option"] as? Int == 2)
        #expect(DeskAction.answer(question: "D14", .text("wait")).request.body?["text"] as? String == "wait")
        #expect(DeskAction.message("hi").request.path == "v1/desk/messages")
        #expect(DeskAction.takeover(session: "7a8b").request.path == "v1/sessions/7a8b/takeover")
        #expect(DeskAction.takeover(session: "7a8b").request.body == nil)
        #expect(DeskAction.giveback(session: "7a8b").request.path == "v1/sessions/7a8b/giveback")
        #expect(DeskAction.mode(track: "114", mode: "parked").request.body?["mode"] as? String == "parked")
        #expect(DeskAction.close(question: "D/1", note: "").request.path == "v1/questions/D%2F1/close")
    }

    @Test func readsServeJSONFromTracksHome() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let file = ServeConfig.file(environment: ["TRACKS_HOME": dir.path])
        #expect(file == dir.appendingPathComponent("serve.json"))
        #expect(throws: ClientError.notRunning(path: file.path)) { try ServeConfig.load(from: file) }
        try Data(#"{"url": "http://127.0.0.1:4711", "token": "t", "pid": 9, "version": 1}"#.utf8).write(to: file)
        #expect(try ServeConfig.load(from: file) == ServeConfig(url: URL(string: "http://127.0.0.1:4711")!, token: "t", pid: 9, version: 1))
        #expect(ServeConfig.file(environment: [:]).path.hasSuffix(".local/state/tracks/serve.json"))
    }
}

@MainActor
@Suite struct HandledUndoTests {
    @Test func aHandledCloseIsReopenedThroughTheUndoRoute() async throws {
        #expect(DeskAction.undoHandled(id: "e7").request.path == "v1/handled/e7/undo")
        #expect(DeskAction.undoHandled(id: "e7").request.body == nil)

        let closed = Handled(id: "e7", track: "cleanup", kind: "closed", did: "merged cleanup")
        let nudged = Handled(id: "e8", track: "cleanup", kind: "nudged", did: "reminded")
        #expect(closed.canUndo && closed.undoLabel == "Reopen")
        #expect(!nudged.canUndo)

        let client = FixtureClient(snapshot: Snapshot(seq: 1, handled: [closed, nudged]))
        let store = DeskStore(client: client)
        await store.undoHandled("e7")
        // The change reaches the store as a stream event in the app; here the fixture's own snapshot shows it.
        let entry = await client.snapshot().handled.first { $0.id == "e7" }
        #expect(entry?.undone == true)
        #expect(entry?.canUndo == false)
        #expect(store.notice?.text == "Reopened cleanup: its card is back, enter opens it")
        #expect(store.notice?.isError == false)

        await store.undoHandled("e7")
        #expect(store.notice?.isError == true)
        await store.undoHandled("e8")
        #expect(store.notice?.isError == true)
    }
}

@MainActor
@Suite struct RestartDeskTests {
    @Test func restartGoesToTheDeskRouteAndIsRefusedMidTurn() async throws {
        #expect(DeskAction.restartDesk.request.path == "v1/desk/restart")
        #expect(DeskAction.restartDesk.request.body == nil)

        var snap = Snapshot(seq: 1)
        snap.desk.status = "idle"
        let idle = DeskStore(client: FixtureClient(snapshot: snap))
        await idle.restartDesk()
        #expect(idle.notice?.text == "the desk is restarting")
        #expect(idle.notice?.isError == false)

        snap.desk.status = "working"
        let busy = DeskStore(client: FixtureClient(snapshot: snap))
        await busy.restartDesk()
        #expect(busy.notice?.isError == true)
    }
}

@Suite struct ApplyTests {
    @Test func appliesChangesInOrderAndReportsGaps() throws {
        var s = try loadFixture()
        let q = try event(#"{"seq": 1043, "op": "upsert", "kind": "question", "value": {"id": "D14", "question": "Merge PR #418 in pantry-api now?", "options": [{"label": "Merge"}], "answer": {"option": 0, "by": "emil"}}}"#)
        #expect(s.apply(q) == .applied)
        #expect(s.question("D14")?.isOpen == false)
        #expect(s.openQuestions.map(\.id) == ["D15"])

        #expect(s.apply(q) == .stale)
        let m = try event(#"{"seq": 1044, "op": "upsert", "kind": "conversation", "value": {"id": "m90", "at": "2026-11-24T13:33:00.123Z", "role": "desk", "text": "Merging.", "steps": 2}}"#)
        #expect(s.apply(m) == .applied)
        #expect(s.conversation.last?.id == "m90")

        let removal = try event(#"{"seq": 1045, "op": "remove", "kind": "session", "value": {"id": "7a8b070e-2c1d-4e5f-9a0b-c1d2e3f4a5b6"}}"#)
        #expect(s.apply(removal) == .applied)
        #expect(s.session("7a8b070e-2c1d-4e5f-9a0b-c1d2e3f4a5b6") == nil)

        let unknown = try event(#"{"seq": 1046, "op": "upsert", "kind": "lane", "value": {"id": "x"}}"#)
        #expect(s.apply(unknown) == .applied)
        #expect(s.seq == 1046)

        #expect(s.apply(trackEvent(1048, name: "skipped")) == .gap)
        #expect(s.seq == 1046)
        #expect(s.track("t1048") == nil)
    }

    @Test func upsertReplacesInPlace() throws {
        var s = try loadFixture()
        var t = try #require(s.track("121"))
        t.mode = "parked"
        let index = s.tracks.firstIndex { $0.id == "121" }
        #expect(s.apply(StreamEvent(seq: 1043, change: .track(t))) == .applied)
        #expect(s.tracks.firstIndex { $0.id == "121" } == index)
        #expect(s.track("121")?.mode == "parked")
    }
}

@MainActor
@Suite(.timeLimit(.minutes(1))) struct StoreSyncTests {
    @Test func refetchesTheSnapshotOnAGap() async throws {
        let first = Snapshot(seq: 10)
        let second = Snapshot(seq: 20, tracks: [Track(id: "t20", name: "after-gap")])
        let client = ScriptedClient(
            snapshots: [first, second],
            batches: [[trackEvent(11, name: "a"), trackEvent(12, name: "b"), trackEvent(14, name: "after a gap"), trackEvent(15, name: "never")],
                      [trackEvent(21, name: "c")]]
        )
        let store = DeskStore(client: client)
        #expect(try await store.syncOnce() == .gap)
        #expect(store.snapshot.seq == 12)
        #expect(store.snapshot.tracks.map(\.name) == ["a", "b"])

        #expect(try await store.syncOnce() == .closed)
        #expect(await client.snapshotFetches == 2)
        #expect(await client.subscriptions == [10, 20])
        #expect(store.snapshot.seq == 21)
        #expect(store.snapshot.tracks.map(\.name) == ["after-gap", "c"])
    }

    @Test func runReconnectsWithASnapshotAfterTheStreamCloses() async throws {
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1), Snapshot(seq: 5), Snapshot(seq: 9)], batches: [[trackEvent(2, name: "a")], [trackEvent(6, name: "b")]])
        let sleeps = SleepLog()
        let store = DeskStore(client: client, sleep: { d in
            if await sleeps.record(d) >= 2 { throw CancellationError() }
        })
        await store.run()
        #expect(await client.snapshotFetches == 2)
        #expect(await client.subscriptions == [1, 5])
        #expect(store.snapshot.seq == 6)
        #expect(store.connection == .live)
        #expect(await sleeps.delays == [.seconds(1), .seconds(1)])
    }

    @Test func goesOfflineAndBacksOffWhenTheServerIsDown() async throws {
        let client = ScriptedClient(snapshots: [], batches: [])
        let sleeps = SleepLog()
        let store = DeskStore(client: client, sleep: { d in
            if await sleeps.record(d) >= 3 { throw CancellationError() }
        })
        await store.run()
        #expect(store.connection == .offline("Prism server isn't running (no test)."))
        #expect(await sleeps.delays == [.seconds(1), .seconds(2), .seconds(4)])
    }

    @Test func notifiesOnlyForQuestionsThatArriveAfterTheFirstLoad() async throws {
        let open = Question(id: "D1", question: "Old?", options: [Option(label: "Yes")])
        let fresh = Question(id: "D2", question: "New?", options: [Option(label: "Yes")])
        var answered = fresh
        answered.answer = AnswerRecord(option: 0)
        let client = ScriptedClient(
            snapshots: [Snapshot(seq: 1, questions: [open])],
            batches: [[StreamEvent(seq: 2, change: .question(fresh)), StreamEvent(seq: 3, change: .question(fresh)), StreamEvent(seq: 4, change: .question(answered))]]
        )
        let store = DeskStore(client: client)
        var raised: [String] = []
        var closed: [String] = []
        store.onNewQuestion = { raised.append($0.id) }
        store.onQuestionClosed = { closed.append($0) }
        _ = try await store.syncOnce()
        #expect(raised == ["D2"])
        #expect(closed == ["D2"])
    }
}

actor SleepLog {
    private(set) var delays: [Duration] = []
    func record(_ d: Duration) -> Int {
        delays.append(d)
        return delays.count
    }
}

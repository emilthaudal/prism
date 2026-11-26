import Foundation
@testable import DeskCore

/// The repo's mock/serve-fixture.json, found from this file's path.
let fixtureURL = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    .appendingPathComponent("mock/serve-fixture.json")

func loadFixture() throws -> Snapshot {
    try DeskJSON.decoder().decode(Snapshot.self, from: Data(contentsOf: fixtureURL))
}

func event(_ json: String) throws -> StreamEvent {
    try DeskJSON.decoder().decode(StreamEvent.self, from: Data(json.utf8))
}

/// A clock tests move by hand.
final class TestClock: @unchecked Sendable {
    private let lock = NSLock()
    private var current: Date

    init(_ start: Date = Date(timeIntervalSince1970: 1_800_000_000)) { current = start }

    var now: Date { lock.withLock { current } }
    func advance(_ seconds: TimeInterval) { lock.withLock { current = current.addingTimeInterval(seconds) } }
}

/// Serves scripted snapshots and event batches, one per connection, and records what was asked of it.
actor ScriptedClient: DeskClient {
    private var snapshots: [Snapshot]
    private var batches: [[StreamEvent]]
    private(set) var snapshotFetches = 0
    private(set) var subscriptions: [Int] = []
    private(set) var actions: [DeskAction] = []
    var failNextAction: ClientError?
    var reply = ActionReply()

    init(snapshots: [Snapshot], batches: [[StreamEvent]]) {
        self.snapshots = snapshots
        self.batches = batches
    }

    func snapshot() throws -> Snapshot {
        snapshotFetches += 1
        guard !snapshots.isEmpty else { throw ClientError.notRunning(path: "test") }
        return snapshots.count > 1 ? snapshots.removeFirst() : snapshots[0]
    }

    nonisolated func events(since seq: Int) -> AsyncThrowingStream<StreamEvent, any Error> {
        let (stream, continuation) = AsyncThrowingStream<StreamEvent, any Error>.makeStream()
        Task {
            for e in await self.nextBatch(since: seq) { continuation.yield(e) }
            continuation.finish()
        }
        return stream
    }

    private func nextBatch(since seq: Int) -> [StreamEvent] {
        subscriptions.append(seq)
        return batches.isEmpty ? [] : batches.removeFirst()
    }

    func transcript(session: String) -> Transcript { Transcript(entries: []) }

    func setFailure(_ error: ClientError?) { failNextAction = error }

    func perform(_ action: DeskAction) throws -> ActionReply {
        actions.append(action)
        if let e = failNextAction { failNextAction = nil; throw e }
        if case .takeover = action { return ActionReply(command: "claude attach abc") }
        return reply
    }

    func setReply(_ reply: ActionReply) { self.reply = reply }
}

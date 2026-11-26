import Foundation

/// Serves a fixture snapshot from memory and applies actions to it, as `prism server --mock FILE` does, so the app runs
/// before the server exists. Each action becomes the events a server would send.
public actor FixtureClient: DeskClient {
    private var state: Snapshot
    private let transcripts: [String: Transcript]
    private let clock: @Sendable () -> Date
    private var subscribers: [UUID: AsyncThrowingStream<StreamEvent, any Error>.Continuation] = [:]

    public init(snapshot: Snapshot, transcripts: [String: Transcript] = [:], clock: @escaping @Sendable () -> Date = { Date() }) {
        state = snapshot
        self.transcripts = transcripts
        self.clock = clock
    }

    /// Reads the snapshot and, when it sits beside it, `serve-transcripts.json` (session id to transcript).
    public static func load(_ file: URL, clock: @escaping @Sendable () -> Date = { Date() }) throws -> FixtureClient {
        let decoder = DeskJSON.decoder()
        let snapshot = try decoder.decode(Snapshot.self, from: try Data(contentsOf: file))
        let side = file.deletingLastPathComponent().appendingPathComponent("serve-transcripts.json")
        let transcripts = (try? Data(contentsOf: side)).flatMap { try? decoder.decode([String: Transcript].self, from: $0) } ?? [:]
        return FixtureClient(snapshot: snapshot, transcripts: transcripts, clock: clock)
    }

    public func snapshot() -> Snapshot { state }

    public func transcript(session: String) -> Transcript {
        transcripts[session] ?? Transcript(entries: [])
    }

    public nonisolated func events(since seq: Int) -> AsyncThrowingStream<StreamEvent, any Error> {
        let (stream, continuation) = AsyncThrowingStream<StreamEvent, any Error>.makeStream()
        let id = UUID()
        Task { await self.subscribe(id, continuation) }
        continuation.onTermination = { _ in Task { await self.unsubscribe(id) } }
        return stream
    }

    private func subscribe(_ id: UUID, _ continuation: AsyncThrowingStream<StreamEvent, any Error>.Continuation) {
        subscribers[id] = continuation
    }

    private func unsubscribe(_ id: UUID) { subscribers[id] = nil }

    private func emit(_ change: StreamEvent.Change) {
        let event = StreamEvent(seq: state.seq + 1, change: change)
        _ = state.apply(event)
        for continuation in subscribers.values { continuation.yield(event) }
    }

    private func nextID(prefix: String, in ids: [String]) -> String {
        let numbers = ids.compactMap { $0.hasPrefix(prefix) ? Int($0.dropFirst(prefix.count)) : nil }
        return "\(prefix)\((numbers.max() ?? 0) + 1)"
    }

    public func perform(_ action: DeskAction) throws -> ActionReply {
        let now = clock()
        switch action {
        case .message(let text):
            emit(.conversation(ConversationEntry(id: nextID(prefix: "m", in: state.conversation.map(\.id)), at: now, role: "emil", text: text)))
            var desk = state.desk
            desk.queuedEvents = (desk.queuedEvents ?? 0) + 1
            emit(.desk(desk))
        case .answer(let id, let choice):
            guard var q = state.question(id) else { throw ClientError.http(status: 404, message: "no question \(id)") }
            guard q.isOpen else { throw ClientError.http(status: 409, message: "\(id) is already answered") }
            switch choice {
            case .option(let n):
                guard q.options.indices.contains(n) else { throw ClientError.http(status: 400, message: "\(id) has no option \(n + 1)") }
                q.answer = AnswerRecord(option: n, by: "emil", at: now)
            case .text(let text):
                q.answer = AnswerRecord(text: text, by: "emil", at: now)
            }
            // As the server does: an agent that is working cannot take the answer now, so it is recorded and waits.
            let busy = q.from == "agent" && q.session.flatMap { state.session($0) }?.state == .working
            if busy { q.delivery = "queued" }
            emit(.question(q))
            if busy { return ActionReply(delivery: "queued", status: "queued \(id)") }
        case .answers(let items):
            guard !items.isEmpty else { throw ClientError.http(status: 400, message: "no answers") }
            var busy = false
            var questions: [Question] = []
            for item in items {
                guard var q = state.question(item.question) else { throw ClientError.http(status: 404, message: "no question \(item.question)") }
                guard q.isOpen else { throw ClientError.http(status: 409, message: "\(item.question) is already answered") }
                guard q.asker == (questions.first ?? q).asker else { throw ClientError.http(status: 400, message: "a batch is one asker's") }
                switch item.choice {
                case .option(let n):
                    guard q.options.indices.contains(n) else { throw ClientError.http(status: 400, message: "\(q.id) has no option \(n + 1)") }
                    q.answer = AnswerRecord(option: n, by: "emil", at: now)
                case .text(let text):
                    q.answer = AnswerRecord(text: text, by: "emil", at: now)
                }
                questions.append(q)
            }
            for var q in questions {
                if q.from == "agent", q.session.flatMap({ state.session($0) })?.state == .working { q.delivery = "queued"; busy = true }
                emit(.question(q))
            }
            let status = "\(busy ? "queued" : "answered") \(items.map(\.question).joined(separator: ", "))"
            return ActionReply(delivery: busy ? "queued" : "delivered", status: status)
        case .ask(let id, _), .showOptions(let id):
            guard let q = state.question(id) else { throw ClientError.http(status: 404, message: "no question \(id)") }
            guard q.isOpen else { throw ClientError.http(status: 409, message: "\(id) is already answered") }
            let busy = q.session.flatMap { state.session($0) }?.state == .working
            return ActionReply(delivery: busy ? "queued" : "delivered", status: "asked about \(id)")
        case .close(let id, let note):
            guard var q = state.question(id) else { throw ClientError.http(status: 404, message: "no question \(id)") }
            q.answer = AnswerRecord(by: "emil", at: now, note: note.isEmpty ? "closed" : note)
            emit(.question(q))
        case .takeover(let id):
            let s = try changeSession(id) { $0.owner = "emil" }
            return ActionReply(command: "claude attach \(s.shortID)")
        case .giveback(let id):
            try changeSession(id) { $0.owner = "desk" }
        case .seen(let id):
            try changeSession(id) { $0.seenAt = now }
        case .stop(let id):
            try changeSession(id) { $0.status = SessionStatus.stopped.rawValue; $0.host = "stopped"; $0.activity = nil }
        case .mode(let id, let mode, let swap):
            guard var t = state.track(id) else { throw ClientError.http(status: 404, message: "no track \(id)") }
            if mode == "focus", t.mode != "focus" {
                let others = state.tracks.filter { $0.mode == "focus" && $0.id != id }.sorted { (Int($0.id) ?? 0) < (Int($1.id) ?? 0) }
                if others.count >= 2 {
                    guard let swap, var out = others.first(where: { $0.name == swap }) else {
                        throw ClientError.focusFull(message: "refused: focus is full", focus: others.map(\.name))
                    }
                    out.mode = "background"
                    out.shownMode = nil
                    out.updatedAt = now
                    emit(.track(out))
                }
            }
            t.mode = mode
            t.shownMode = nil
            t.updatedAt = now
            emit(.track(t))
        case .open(let id):
            guard state.track(id) != nil else { throw ClientError.http(status: 404, message: "no track \(id)") }
        case .undoHandled(let id):
            guard var h = state.handled.first(where: { $0.id == id }) else { throw ClientError.http(status: 404, message: "no handled entry \(id)") }
            guard h.undone != true else { throw ClientError.http(status: 409, message: "that was taken back already") }
            guard h.canUndo else { throw ClientError.http(status: 400, message: "only the desk's answers, starts and closes can be taken back") }
            h.undone = true
            emit(.handled(h))
            return ActionReply(status: h.kind == "closed" ? "Reopened \(h.track ?? "the track"): its card is back, enter opens it" : "Took back the desk's \(h.kind ?? "entry")")
        case .restartDesk:
            // As the server does: a desk in a turn is not cut off.
            guard state.desk.status != "working" else { throw ClientError.http(status: 409, message: "the desk is in a turn: restart it when it is idle") }
            return ActionReply(status: "the desk is restarting")
        }
        return ActionReply()
    }

    @discardableResult
    private func changeSession(_ id: String, _ change: (inout Session) -> Void) throws -> Session {
        guard var s = state.session(id) else { throw ClientError.http(status: 404, message: "no session \(id)") }
        change(&s)
        s.updatedAt = clock()
        emit(.session(s))
        return s
    }

    /// Applies a change as if the server had sent it, for a demo script.
    public func inject(_ change: StreamEvent.Change) { emit(change) }

    /// Adds a question as if a session had just asked it, to try notifications without a server.
    public func raise(_ question: Question) {
        var q = question
        if q.id.isEmpty { q.id = nextID(prefix: "D", in: state.questions.map(\.id)) }
        q.createdAt = q.createdAt ?? clock()
        emit(.question(q))
    }
}

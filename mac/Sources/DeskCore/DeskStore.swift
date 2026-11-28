import Foundation
import Observation

public enum Connection: Equatable, Sendable {
    case connecting
    case live
    case offline(String)
    /// prism server could not be started; waits for a retry.
    case startFailed(String)
}

/// A track waiting to enter a full focus, and the tracks in it (older first) one of which must leave.
public struct FocusSwap: Equatable, Sendable {
    public var trackID: String
    public var trackName: String
    public var focus: [Track]
}

/// Something to tell Emil for a moment: an error, or the command to run when Rex isn't there to take a session over.
public struct Notice: Equatable, Sendable {
    public var text: String
    public var command: String?
    public var isError: Bool
    /// An SF Symbol for a quiet notice that is neither an error nor a command to run.
    public var symbol: String?

    public init(text: String, command: String? = nil, isError: Bool = false, symbol: String? = nil) {
        self.text = text
        self.command = command
        self.isError = isError
        self.symbol = symbol
    }
}

/// The app's state: the snapshot kept current from the event stream, the answers counting down, and the actions.
@MainActor
@Observable
public final class DeskStore {
    public private(set) var snapshot = Snapshot.empty {
        didSet { boardCache = nil }
    }
    public private(set) var loaded = false
    public private(set) var connection = Connection.connecting
    public private(set) var answers = AnswerQueue()
    /// Answers chosen but not sent: an asker with several open questions gets them as one message once each is answered.
    public private(set) var staged: [String: AnswerChoice] = [:]
    /// Questions "Show me the options" was pressed for: the button shows it is pending and ignores another click.
    public private(set) var optionsRequested: Set<String> = []
    public var notice: Notice?
    /// Set when a track cannot go to focus because focus is full: Emil picks which focus track leaves.
    public var focusSwap: FocusSwap?

    /// A question that arrived after the first load and is still open.
    @ObservationIgnored public var onNewQuestion: ((Question) -> Void)?
    /// A question that is no longer open, so its notification can go.
    @ObservationIgnored public var onQuestionClosed: ((String) -> Void)?

    /// Runs before every connection: starts prism server when it isn't running.
    @ObservationIgnored public var prepare: (@Sendable () async throws -> Void)?
    /// Set by the app: starts the sync again after a failed start.
    @ObservationIgnored public var onRetry: (() -> Void)?

    @ObservationIgnored public let client: any DeskClient
    @ObservationIgnored public let clock: @Sendable () -> Date
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var known: Set<String> = []
    @ObservationIgnored private var boardCache: Board?

    /// The snapshot's board, worked out once per change of the snapshot. Each question card asked for its dialog and
    /// staged answers, and each ask built a Board of its own: 30 open questions sorted some 30 times per redraw.
    public var board: Board {
        let s = snapshot
        if let cached = boardCache { return cached }
        let fresh = Board(s)
        boardCache = fresh
        return fresh
    }

    static let retryDelays: [Duration] = [.seconds(1), .seconds(2), .seconds(4), .seconds(8), .seconds(15)]

    public init(client: any DeskClient, clock: @escaping @Sendable () -> Date = { Date() }, sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.client = client
        self.clock = clock
        self.sleep = sleep
    }

    public var now: Date { clock() }

    public func retry() { onRetry?() }

    // MARK: Sync

    public enum SyncEnd: Equatable, Sendable {
        /// A change was missed; fetch the snapshot again at once.
        case gap
        /// The stream closed; reconnect.
        case closed
    }

    /// Follows the server until cancelled: snapshot, then changes; a gap refetches at once, a lost connection after a
    /// growing pause. Before each connection `prepare` starts serve again if it is gone (killed, or exited for any
    /// reason), so a lost serve recovers on its own instead of leaving the app offline.
    public func run() async {
        if prepare != nil { connection = .connecting }
        var failures = 0
        while !Task.isCancelled {
            do {
                if let prepare {
                    do { try await prepare() } catch is CancellationError { throw CancellationError() } catch where !loaded {
                        // At launch a failed start waits for Retry; once loaded, it is retried like a lost connection.
                        connection = .startFailed(error.localizedDescription)
                        return
                    }
                }
                let end = try await syncOnce()
                failures = 0
                if end == .gap { continue }
            } catch is CancellationError {
                return
            } catch {
                connection = .offline(error.localizedDescription)
                failures += 1
            }
            if Task.isCancelled { return }
            let delay = Self.retryDelays[min(max(failures - 1, 0), Self.retryDelays.count - 1)]
            do { try await sleep(delay) } catch { return }
        }
    }

    /// One connection: fetch the snapshot, then apply changes until a gap or the end of the stream.
    public func syncOnce() async throws -> SyncEnd {
        adopt(try await client.snapshot())
        connection = .live
        for try await event in client.events(since: snapshot.seq) {
            if apply(event) == .gap { return .gap }
        }
        return .closed
    }

    func adopt(_ new: Snapshot) {
        let wasLoaded = loaded
        let open = Set(snapshot.openQuestions.map(\.id))
        snapshot = new
        loaded = true
        for q in new.openQuestions where wasLoaded && !known.contains(q.id) {
            onNewQuestion?(q)
        }
        let stillOpen = Set(new.openQuestions.map(\.id))
        for id in open.subtracting(stillOpen) { onQuestionClosed?(id) }
        known = Set(new.questions.map(\.id))
        answers.drop { !stillOpen.contains($0) }
        staged = staged.filter { stillOpen.contains($0.key) }
    }

    @discardableResult
    func apply(_ event: StreamEvent) -> ApplyOutcome {
        let outcome = snapshot.apply(event)
        guard outcome == .applied else { return outcome }
        switch event.change {
        case .question(let q):
            if q.isOpen, !known.contains(q.id) { onNewQuestion?(q) }
            known.insert(q.id)
            if !q.isOpen {
                answers.drop { $0 == q.id }
                staged[q.id] = nil
                onQuestionClosed?(q.id)
            }
        case .remove("question", let id):
            answers.drop { $0 == id }
            staged[id] = nil
            onQuestionClosed?(id)
        default: break
        }
        return outcome
    }

    // MARK: Answering

    /// A permission prompt is answered on its own and never batched.
    private func isPermission(_ q: Question) -> Bool {
        q.session.flatMap { snapshot.session($0) }?.waitsOnPermission == true
    }

    /// The open questions of `q`'s dialog that an answer can still be staged for: not counting down, not permission
    /// prompts. A question with no track and no session is the desk's own, and each is answered alone.
    public func batchGroup(of q: Question) -> [Question] {
        dialog(of: q).filter { answers.pending(for: $0.id) == nil && answers.batch(containing: $0.id) == nil }
    }

    /// The open questions shown with `q`, `q` among them, in merge, install, close order: a track's decisions are one
    /// dialog. A permission prompt and the desk's own questions are shown alone.
    public func dialog(of q: Question) -> [Question] {
        guard !isPermission(q), q.track != nil || q.session != nil else { return [q] }
        return board.needsYou.filter { $0.dialog == q.dialog && !isPermission($0) }
    }

    /// The question of `q`'s dialog that the keys answer: `q` while it is undecided, else the first undecided one.
    public func nextUndecided(from q: Question) -> Question {
        let undecided = { (x: Question) in self.staged[x.id] == nil && self.answers.pending(for: x.id) == nil && self.answers.batch(containing: x.id) == nil }
        if undecided(q) { return q }
        return dialog(of: q).first(where: undecided) ?? q
    }

    /// Every question but a permission prompt can be asked about.
    public func canAsk(_ q: Question) -> Bool { q.isOpen && !isPermission(q) }

    /// Whether answering `q` stages the answer instead of sending it.
    public func stagesAnswer(to q: Question) -> Bool { batchGroup(of: q).count > 1 }

    public func stagedChoice(for questionID: String) -> AnswerChoice? { staged[questionID] }

    /// The staged answers of `dialog`, in the board's order.
    public func stagedItems(for dialog: String) -> [BatchItem] {
        board.needsYou.compactMap { q in q.dialog == dialog ? staged[q.id].map { c in BatchItem(question: q.id, choice: c, againstRecommendation: q.recommendedChoice.map { $0 != c } ?? false) } : nil }
    }

    public func stagedCount(for dialog: String) -> Int { stagedItems(for: dialog).count }

    /// The dialog ⌘↩ sends for: `preferring`'s, else the first with a staged answer.
    public func stagedDialog(preferring questionID: String?) -> String? {
        if let id = questionID, let q = snapshot.question(id), stagedCount(for: q.dialog) > 0 { return q.dialog }
        return board.needsYou.first { staged[$0.id] != nil }?.dialog
    }

    /// Starts the 5-second countdown; `tick` sends it when it runs out. A dialog with other open questions has the answer
    /// staged instead; the countdown for all of its staged answers starts when its last open question is staged.
    public func answer(_ questionID: String, choice: AnswerChoice) {
        guard let q = snapshot.question(questionID), q.isOpen else { return }
        if case .option(let n) = choice, !q.options.indices.contains(n) { return }
        if case .text(let t) = choice, t.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return }
        let group = batchGroup(of: q)
        guard group.count > 1 else {
            staged[questionID] = nil
            sendWhenDue(answers.start(q, choice: choice, now: clock()).deadline)
            return
        }
        staged[questionID] = choice
        if group.allSatisfy({ staged[$0.id] != nil }) { sendStaged(dialog: q.dialog) }
    }

    /// Takes a staged answer back to undecided.
    public func unstage(_ questionID: String) { staged[questionID] = nil }

    /// Starts the countdown for everything staged for `dialog`, though some of its questions are still open.
    public func sendStaged(dialog: String) {
        let items = stagedItems(for: dialog)
        guard !items.isEmpty else { return }
        for item in items { staged[item.question] = nil }
        sendWhenDue(answers.startBatch(dialog: dialog, items: items, now: clock()).deadline)
    }

    /// The periodic tick sends up to its interval late; this one lands on the deadline, so the card leaves when the countdown says 0.
    private func sendWhenDue(_ deadline: Date) {
        Task { [weak self] in
            while let store = self, store.clock() < deadline {
                try? await Task.sleep(for: .seconds(max(0.001, deadline.timeIntervalSince(store.clock()))))
            }
            await self?.tick()
        }
    }

    public func answerRecommended(_ questionID: String) {
        guard let choice = snapshot.question(questionID)?.recommendedChoice else { return }
        answer(questionID, choice: choice)
    }

    /// Takes back the newest answer (or the one to `questionID`) before it is sent.
    @discardableResult
    public func undo(_ questionID: String? = nil) -> Bool {
        if questionID == nil ? answers.newestIsBatch : answers.batch(containing: questionID!) != nil {
            guard let batch = answers.undoBatch(containing: questionID) else { return false }
            for item in batch.items where snapshot.question(item.question)?.isOpen == true { staged[item.question] = item.choice }
            return true
        }
        return answers.undo(questionID) != nil
    }

    /// Sends the answers whose undo window has passed. Reads before it writes: `takeDue` mutates `answers`, and any write
    /// to it, even one that removes nothing, redraws every view that reads it, five times a second.
    public func tick() async {
        let now = clock()
        guard answers.items.contains(where: { $0.deadline <= now }) || answers.batches.contains(where: { $0.deadline <= now }) else { return }
        for due in answers.takeDue(now: clock()) {
            await sendAnswer(due.question, due.choice)
        }
        for due in answers.takeDueBatches(now: clock()) {
            await sendBatch(due)
        }
    }

    /// Sends a dialog's answers as one message to each asker in it: the track's agent gets the answers to its asks
    /// together, the desk those to the decisions it raised about the track.
    private func sendBatch(_ batch: PendingBatch) async {
        let track = batch.items.first.flatMap { snapshot.question($0.question)?.track }.flatMap { snapshot.track($0)?.name }
        var askers: [String] = []
        var byAsker: [String: [BatchItem]] = [:]
        for item in batch.items {
            let asker = snapshot.question(item.question)?.asker ?? item.question
            if byAsker[asker] == nil { askers.append(asker) }
            byAsker[asker, default: []].append(item)
        }
        var queued = false
        for asker in askers {
            guard let items = byAsker[asker] else { continue }
            let held = items.compactMap { hold($0.question, $0.choice) }
            let action: DeskAction = items.count == 1 ? .answer(question: items[0].question, items[0].choice) : .answers(items)
            if let reply = await send(action) {
                if reply.delivery == "queued" { queued = true }
            } else {
                restore(held)
            }
        }
        if queued { notice = Notice(text: "Queued for \(track ?? batch.dialog): sent when its agent stops", symbol: "clock") }
    }

    /// Asks the agent about its question; the question stays open.
    public func ask(_ questionID: String, _ text: String) async {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        await sendAsk(questionID, .ask(question: questionID, text: text))
    }

    /// Asks the agent to render each option to an image and ask again.
    /// A repeat call while the first is out, or after it went through, does nothing.
    public func showOptions(_ questionID: String) async {
        guard !optionsRequested.contains(questionID) else { return }
        optionsRequested.insert(questionID)
        if !(await sendAsk(questionID, .showOptions(question: questionID))) { optionsRequested.remove(questionID) }
    }

    @discardableResult
    private func sendAsk(_ questionID: String, _ action: DeskAction) async -> Bool {
        let q = snapshot.question(questionID)
        let track = q?.track.flatMap { snapshot.track($0)?.name } ?? questionID
        guard let reply = await send(action) else { return false }
        notice = reply.delivery == "queued"
            ? Notice(text: "Queued for \(track): sent when its agent stops", symbol: "clock")
            : Notice(text: "Asked \(track) about \(questionID)", symbol: "questionmark.bubble")
        return true
    }

    /// Sends at once, with no countdown: for a notification, which is its own confirmation.
    public func answerNow(_ questionID: String, choice: AnswerChoice) async {
        answers.undo(questionID)
        await sendAnswer(questionID, choice)
    }

    /// Sends an answer; one recorded while the agent was busy says so quietly, as it is no failure.
    private func sendAnswer(_ questionID: String, _ choice: AnswerChoice) async {
        let track = snapshot.question(questionID)?.track.flatMap { snapshot.track($0)?.name }
        let held = hold(questionID, choice)
        guard let reply = await send(.answer(question: questionID, choice)) else {
            restore(held.map { [$0] } ?? [])
            return
        }
        guard reply.delivery == "queued" else { return }
        notice = Notice(text: "Queued for \(track ?? questionID): sent when its agent stops", symbol: "clock")
    }

    /// Marks a question answered in the snapshot as its answer goes out, so its card leaves at once instead of coming
    /// back until the server's own update arrives (EMI-425). Returns the question as it was, for `restore`.
    private func hold(_ id: String, _ choice: AnswerChoice) -> Question? {
        guard let i = snapshot.questions.firstIndex(where: { $0.id == id && $0.isOpen }) else { return nil }
        let before = snapshot.questions[i]
        switch choice {
        case .option(let n): snapshot.questions[i].answer = AnswerRecord(option: n, by: "emil", at: clock())
        case .text(let t): snapshot.questions[i].answer = AnswerRecord(text: t, by: "emil", at: clock())
        }
        return before
    }

    /// Puts questions back as open when their answer did not go out.
    private func restore(_ held: [Question]) {
        for q in held {
            guard let i = snapshot.questions.firstIndex(where: { $0.id == q.id }), !snapshot.questions[i].isOpen else { continue }
            snapshot.questions[i] = q
        }
    }

    // MARK: Other actions

    /// Whether the message went out; on a failure `notice` says why.
    @discardableResult
    public func sendMessage(_ text: String) async -> Bool {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return false }
        return await send(.message(text)) != nil
    }

    public func takeOver(_ sessionID: String) async {
        guard let reply = await send(.takeover(session: sessionID)) else { return }
        if let command = reply.command {
            notice = Notice(text: "Rex isn't running. Take it over in a terminal:", command: command)
        }
    }

    public func stopLoop(_ trackID: String) async { await send(.stopLoop(track: trackID)) }
    public func giveBack(_ sessionID: String) async { await send(.giveback(session: sessionID)) }
    public func stop(_ sessionID: String) async { await send(.stop(session: sessionID)) }
    /// Sets a track's mode. A third focus track is refused by the server, and `focusSwap` then holds
    /// the question of which focus track moves to background; `confirmFocusSwap` answers it.
    public func setMode(_ trackID: String, _ mode: String, swap: String? = nil) async {
        do {
            _ = try await client.perform(.mode(track: trackID, mode: mode, swap: swap))
        } catch ClientError.focusFull(_, let names) {
            let focus = names.compactMap { n in snapshot.tracks.first { $0.name == n } }
            focusSwap = FocusSwap(trackID: trackID, trackName: snapshot.track(trackID)?.name ?? trackID, focus: focus)
        } catch {
            notice = Notice(text: error.localizedDescription, isError: true)
        }
    }

    /// Moves `leaving` (a focus track's name) to background and the waiting track to focus.
    public func confirmFocusSwap(leaving name: String) async {
        guard let swap = focusSwap else { return }
        focusSwap = nil
        await setMode(swap.trackID, "focus", swap: name)
    }

    public func cancelFocusSwap() { focusSwap = nil }
    /// Opens the track in Rex; a track the desk runs in the background is attached to there.
    public func open(_ trackID: String) async {
        guard let reply = await send(.open(track: trackID)) else { return }
        if let command = reply.command {
            notice = Notice(text: "Rex isn't running. Open it in a terminal:", command: command)
        }
    }
    public func close(_ questionID: String, note: String = "") async { await send(.close(question: questionID, note: note)) }

    /// Takes back a handled entry: reopens a track the desk closed. The server's status says what it did.
    public func undoHandled(_ id: String) async {
        guard let reply = await send(.undoHandled(id: id)), let status = reply.status else { return }
        notice = Notice(text: status, symbol: "arrow.uturn.backward")
    }

    /// Restarts the desk with a fresh session; what is queued for it waits for the new one.
    public func restartDesk() async {
        guard let reply = await send(.restartDesk) else { return }
        notice = Notice(text: reply.status ?? "the desk is restarting", symbol: "arrow.clockwise")
    }

    /// Marks a finished session seen when Emil opens it.
    public func opened(_ sessionID: String) async {
        guard let s = snapshot.session(sessionID), s.isUnseenDone else { return }
        await send(.seen(session: sessionID))
    }

    public func transcript(_ sessionID: String) async throws -> Transcript {
        try await client.transcript(session: sessionID)
    }

    @discardableResult
    private func send(_ action: DeskAction) async -> ActionReply? {
        do {
            return try await client.perform(action)
        } catch {
            notice = Notice(text: error.localizedDescription, isError: true)
            return nil
        }
    }
}

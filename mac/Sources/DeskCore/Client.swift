import Foundation

/// How the app reaches `prism server`: the live HTTP client or the in-memory fixture.
public protocol DeskClient: Sendable {
    func snapshot() async throws -> Snapshot
    /// Changes after `seq`, in order. Ends when the connection closes.
    func events(since seq: Int) -> AsyncThrowingStream<StreamEvent, any Error>
    func transcript(session: String) async throws -> Transcript
    @discardableResult func perform(_ action: DeskAction) async throws -> ActionReply
}

public enum AnswerChoice: Equatable, Sendable {
    /// The option's index, from 0, as `recommended` counts.
    case option(Int)
    case text(String)
}

/// The actions in docs/serve-api.md.
public enum DeskAction: Equatable, Sendable {
    case message(String)
    case answer(question: String, AnswerChoice)
    /// One asker's staged answers, sent and delivered as one message.
    case answers([BatchItem])
    /// Asks the agent about its question without answering it.
    case ask(question: String, text: String)
    /// Asks the agent to render each option to an image and ask again.
    case showOptions(question: String)
    case close(question: String, note: String)
    case takeover(session: String)
    case giveback(session: String)
    case seen(session: String)
    case stop(session: String)
    /// `swap` names the focus track that moves to background when focus is full.
    case mode(track: String, mode: String, swap: String? = nil)
    case open(track: String)
    /// Takes back a handled entry, as u does in the pane: reopens a track the desk closed.
    case undoHandled(id: String)
    /// Ends the desk's session and starts a fresh one; refused while the desk is in a turn.
    case restartDesk

    /// The path under the server's URL and the JSON body, if any.
    public var request: (path: String, body: [String: any Sendable]?) {
        switch self {
        case .message(let text): ("v1/desk/messages", ["text": text])
        case .answer(let id, .option(let n)): ("v1/questions/\(Self.escape(id))/answer", ["option": n])
        case .answer(let id, .text(let text)): ("v1/questions/\(Self.escape(id))/answer", ["text": text])
        case .answers(let items): ("v1/answers", ["answers": items.map(\.json)])
        case .ask(let id, let text): ("v1/questions/\(Self.escape(id))/ask", ["text": text])
        case .showOptions(let id): ("v1/questions/\(Self.escape(id))/ask", ["show_options": true])
        case .close(let id, let note): ("v1/questions/\(Self.escape(id))/close", ["note": note])
        case .takeover(let id): ("v1/sessions/\(Self.escape(id))/takeover", nil)
        case .giveback(let id): ("v1/sessions/\(Self.escape(id))/giveback", nil)
        case .seen(let id): ("v1/sessions/\(Self.escape(id))/seen", nil)
        case .stop(let id): ("v1/sessions/\(Self.escape(id))/stop", nil)
        case .mode(let id, let mode, let swap):
            ("v1/tracks/\(Self.escape(id))/mode", swap.map { ["mode": mode, "swap": $0] } ?? ["mode": mode])
        case .open(let id): ("v1/tracks/\(Self.escape(id))/open", nil)
        case .undoHandled(let id): ("v1/handled/\(Self.escape(id))/undo", nil)
        case .restartDesk: ("v1/desk/restart", nil)
        }
    }

    private static func escape(_ id: String) -> String {
        id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? id
    }
}

/// One answer of a batch.
public struct BatchItem: Equatable, Sendable {
    public var question: String
    public var choice: AnswerChoice
    /// Not the recommendation; for the countdown's label only, never sent.
    public var againstRecommendation: Bool

    public init(question: String, choice: AnswerChoice, againstRecommendation: Bool = false) {
        self.question = question
        self.choice = choice
        self.againstRecommendation = againstRecommendation
    }

    /// The flag is for the countdown's label; two items are the same answer without it.
    public static func == (a: BatchItem, b: BatchItem) -> Bool { a.question == b.question && a.choice == b.choice }

    var json: [String: any Sendable] {
        switch choice {
        case .option(let n): ["id": question, "option": n]
        case .text(let text): ["id": question, "text": text]
        }
    }
}

/// What an action answers that the app shows: takeover's command when Rex isn't there.
public struct ActionReply: Equatable, Sendable, Decodable {
    public var command: String?
    /// An answer's outcome: "delivered", or "queued" when the agent was busy and the answer waits for it to stop.
    public var delivery: String?
    public var status: String?

    public init(command: String? = nil, delivery: String? = nil, status: String? = nil) {
        self.command = command
        self.delivery = delivery
        self.status = status
    }
}

public enum ClientError: Error, Equatable, LocalizedError {
    case notRunning(path: String)
    case badServeFile(path: String)
    case http(status: Int, message: String)
    /// A third focus track: the tracks in focus, older first, by name.
    case focusFull(message: String, focus: [String])
    case startFailed(String, log: String)
    case startTimedOut(seconds: Int, log: String)

    public var errorDescription: String? {
        switch self {
        case .notRunning(let path): "Prism server isn't running (no \(path))."
        case .badServeFile(let path): "Can't read \(path)."
        case .startFailed(let message, let log): "Prism server did not start: \(message) (log: \(log))"
        case .startTimedOut(let seconds, let log): "Prism server did not write serve.json within \(seconds) s (log: \(log))"
        case .focusFull(let message, _): message
        case .http(let status, let message): message.isEmpty ? "Prism server answered \(status)." : message
        }
    }
}

/// `$TRACKS_HOME/serve.json`, which `prism server` writes when it starts.
public struct ServeConfig: Decodable, Equatable, Sendable {
    public var url: URL
    public var token: String
    public var pid: Int?
    public var version: Int?

    public static func directory(environment: [String: String] = ProcessInfo.processInfo.environment) -> URL {
        if let home = environment["TRACKS_HOME"], !home.isEmpty { return URL(fileURLWithPath: home) }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".local/state/tracks")
    }

    public static func file(environment: [String: String] = ProcessInfo.processInfo.environment) -> URL {
        directory(environment: environment).appendingPathComponent("serve.json")
    }

    public static func load(from file: URL) throws -> ServeConfig {
        guard let data = try? Data(contentsOf: file) else { throw ClientError.notRunning(path: file.path) }
        do { return try JSONDecoder().decode(ServeConfig.self, from: data) } catch {
            throw ClientError.badServeFile(path: file.path)
        }
    }
}

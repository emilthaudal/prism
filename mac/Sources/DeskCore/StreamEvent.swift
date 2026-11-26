import Foundation

/// One change from `GET /v1/events`: `{"seq", "op", "kind", "value"}`.
public struct StreamEvent: Decodable, Equatable, Sendable {
    public enum Change: Equatable, Sendable {
        case track(Track)
        case session(Session)
        case question(Question)
        case conversation(ConversationEntry)
        case handled(Handled)
        case desk(DeskInfo)
        case remove(kind: String, id: String)
        /// A kind this client doesn't know; it still moves `seq` on.
        case unknown(kind: String)
    }

    public var seq: Int
    public var change: Change

    public init(seq: Int, change: Change) {
        self.seq = seq
        self.change = change
    }

    fileprivate enum CodingKeys: String, CodingKey { case seq, op, kind, value }
    fileprivate struct RemovedID: Decodable { var id: String }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(Int.self, forKey: .seq)
        change = try Change(from: decoder)
    }
}

/// `{"op", "kind", "value"}` without a `seq`, as a demo script writes a change.
extension StreamEvent.Change: Decodable {
    public init(from decoder: any Decoder) throws {
        typealias Keys = StreamEvent.CodingKeys
        let c = try decoder.container(keyedBy: Keys.self)
        let op = try c.decode(String.self, forKey: .op)
        let kind = try c.decode(String.self, forKey: .kind)
        if op == "remove" {
            self = .remove(kind: kind, id: try c.decode(StreamEvent.RemovedID.self, forKey: .value).id)
            return
        }
        switch kind {
        case "track": self = .track(try c.decode(Track.self, forKey: .value))
        case "session": self = .session(try c.decode(Session.self, forKey: .value))
        case "question": self = .question(try c.decode(Question.self, forKey: .value))
        case "conversation": self = .conversation(try c.decode(ConversationEntry.self, forKey: .value))
        case "handled": self = .handled(try c.decode(Handled.self, forKey: .value))
        case "desk": self = .desk(try c.decode(DeskInfo.self, forKey: .value))
        default: self = .unknown(kind: kind)
        }
    }
}

public enum ApplyOutcome: Equatable, Sendable {
    case applied
    /// Already seen (`seq` at or below the snapshot's); ignored.
    case stale
    /// `seq` jumped: changes were missed, so the snapshot must be fetched again.
    case gap
}

extension Snapshot {
    /// The newest entries the snapshot keeps, as the server does.
    public static let conversationLimit = 200

    /// Applies one change in order. Only the change right after `seq` is applied.
    public mutating func apply(_ event: StreamEvent) -> ApplyOutcome {
        if event.seq <= seq { return .stale }
        if event.seq != seq + 1 { return .gap }

        seq = event.seq
        switch event.change {
        case .track(let t): tracks.upsert(t)
        case .session(let s): sessions.upsert(s)
        case .question(let q): questions.upsert(q)
        case .conversation(let m):
            conversation.upsert(m)
            if conversation.count > Self.conversationLimit {
                conversation.removeFirst(conversation.count - Self.conversationLimit)
            }
        case .handled(let h): handled.upsert(h)
        case .desk(let d): desk = d
        case .remove(let kind, let id):
            switch kind {
            case "track": tracks.removeAll { $0.id == id }
            case "session": sessions.removeAll { $0.id == id }
            case "question": questions.removeAll { $0.id == id }
            case "conversation": conversation.removeAll { $0.id == id }
            case "handled": handled.removeAll { $0.id == id }
            default: break
            }
        case .unknown: break
        }
        return .applied
    }
}

extension Array where Element: Identifiable {
    /// Replaces the element with the same id in place, or appends it.
    mutating func upsert(_ element: Element) {
        if let i = firstIndex(where: { $0.id == element.id }) {
            self[i] = element
        } else {
            append(element)
        }
    }
}

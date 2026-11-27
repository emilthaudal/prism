import Foundation

/// One of Emil's own messages and what Prism said back, with no track in between (EMI-488).
public struct DirectAnswer: Equatable, Sendable, Identifiable {
    /// Nil for a kept message of Prism's that answered nothing Emil asked.
    public var question: ConversationEntry?
    public var answer: ConversationEntry
    public var id: String { answer.id }

    /// The entry a jump lands on: the question, so it reads with its answer under it.
    public var anchor: String { question?.id ?? answer.id }

    /// The answer's first line, for a row that names it.
    public var gist: String {
        answer.text.split(separator: "\n", omittingEmptySubsequences: true).first.map { $0.trimmingCharacters(in: .whitespaces) } ?? ""
    }
}

public enum Answers {
    /// Emil's own messages, oldest first: the anchors of the conversation.
    public static func asked(_ conversation: [ConversationEntry]) -> [ConversationEntry] {
        conversation.filter { $0.role == "emil" }
    }

    /// Each of Emil's messages with Prism's first reply to it; reports in between are not replies, and neither is a
    /// reply that was re-asked as decisions.
    public static func replies(_ conversation: [ConversationEntry]) -> [DirectAnswer] {
        var out: [DirectAnswer] = []
        var open: ConversationEntry?
        for entry in conversation {
            switch entry.role {
            case "emil": open = entry
            case "report": continue
            default:
                guard let question = open, entry.outcome != "reasked" else { continue }
                out.append(DirectAnswer(question: question, answer: entry))
                open = nil
            }
        }
        return out
    }

    /// The replies to what Emil asked as a question. A message with no question mark is an instruction ("start a
    /// track for..."), and its reply is an acknowledgement, not something to come back to.
    public static func direct(_ conversation: [ConversationEntry]) -> [DirectAnswer] {
        replies(conversation).filter { $0.question?.text.contains("?") == true }
    }

    /// The messages Emil kept, in the order he kept them, each with the question it answered if it answered one.
    public static func kept(_ ids: [String], in conversation: [ConversationEntry]) -> [DirectAnswer] {
        let byAnswer = Dictionary(replies(conversation).map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        return ids.compactMap { id in
            byAnswer[id] ?? conversation.first { $0.id == id }.map { DirectAnswer(question: nil, answer: $0) }
        }
    }

    /// The message a step lands on: back (-1) from the bottom is the newest, and forward (+1) past the newest is nil,
    /// the bottom again.
    public static func step(from cursor: String?, by delta: Int, in asked: [String]) -> String? {
        guard !asked.isEmpty else { return nil }
        guard let cursor, let i = asked.firstIndex(of: cursor) else { return delta < 0 ? asked.last : nil }
        let next = i + delta
        if next < 0 { return asked.first }
        return next < asked.count ? asked[next] : nil
    }
}

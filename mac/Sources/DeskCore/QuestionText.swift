import Foundation

/// A question laid out for reading: a short title, then the rest as paragraphs and steps. An agent's ask can run to a
/// paragraph with its steps written inline ("(a) … (b) …"), which shown whole in the title's weight is a wall.
public struct QuestionText: Equatable, Sendable {
    public enum Part: Equatable, Sendable {
        case text(String)
        /// A step's marker as written ("a", "2") and its text.
        case step(String, String)

        var length: Int {
            switch self {
            case .text(let s): s.count
            case .step(_, let s): s.count + 4
            }
        }
    }

    /// The first sentence, or the whole question when it is short. Empty when no sentence ends early enough to be one.
    public var title: String
    public var body: [Part]

    /// A question this long or shorter stays one title.
    public static let titleMax = 140
    /// A body past this many characters or parts shows folded until asked for whole.
    static let foldLength = 240
    static let foldParts = 3

    /// The title, or the question itself when it has none: what one line about the question shows.
    public var headline: String {
        if !title.isEmpty { return title }
        return body.map { part in
            switch part {
            case .text(let s): s
            case .step(let m, let s): "(\(m)) \(s)"
            }
        }.joined(separator: " ")
    }

    public var folds: Bool {
        body.count > Self.foldParts || body.reduce(0) { $0 + $1.length } > Self.foldLength
    }

    public init(title: String, body: [Part] = []) {
        self.title = title
        self.body = body
    }

    public init(_ question: String) {
        let lines = question.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        if lines.count > 1, lines[0].count <= Self.titleMax {
            self.init(title: lines[0], body: Self.parts(lines.dropFirst().joined(separator: " ")))
            return
        }
        let text = lines.joined(separator: " ")
        if text.count <= Self.titleMax {
            self.init(title: text)
            return
        }
        if let end = Self.firstSentenceEnd(text) {
            let rest = text[end...].trimmingCharacters(in: .whitespaces)
            self.init(title: String(text[..<end]), body: Self.parts(rest))
        } else {
            self.init(title: "", body: Self.parts(text))
        }
    }

    /// Just past the first `.`, `?`, `!` or `:` that ends a sentence within `titleMax`, outside code spans.
    static func firstSentenceEnd(_ text: String) -> String.Index? {
        var inCode = false
        var i = text.startIndex
        var n = 0
        while i < text.endIndex, n < titleMax {
            let c = text[i]
            let next = text.index(after: i)
            if c == "`" {
                inCode.toggle()
            } else if !inCode, ".?!:".contains(c), next == text.endIndex || text[next] == " " {
                return next
            }
            i = next
            n += 1
        }
        return nil
    }

    /// Splits text at step markers that run in order from (a) or 1), "(a) … (b) …"; two at least, outside code spans.
    static func parts(_ text: String) -> [Part] {
        guard !text.isEmpty else { return [] }
        let code = codeSpans(text)
        var cuts: [(range: Range<String.Index>, label: String)] = []
        for m in text.matches(of: /(?:^|\s)\(?([a-z]|[1-9][0-9]?)\)\s/) {
            let label = String(m.output.1)
            guard !code.contains(where: { $0.contains(m.range.lowerBound) }) else { continue }
            let expected = cuts.last.map { successor($0.label) } ?? nil
            if let expected {
                guard label == expected else { continue }
            } else if label != "a" && label != "1" {
                continue
            }
            cuts.append((m.range, label))
        }
        guard cuts.count >= 2 else { return [.text(text)] }
        var out: [Part] = []
        let lead = text[..<cuts[0].range.lowerBound].trimmingCharacters(in: .whitespaces)
        if !lead.isEmpty { out.append(.text(lead)) }
        for (k, cut) in cuts.enumerated() {
            let end = k + 1 < cuts.count ? cuts[k + 1].range.lowerBound : text.endIndex
            out.append(.step(cut.label, text[cut.range.upperBound..<end].trimmingCharacters(in: .whitespaces)))
        }
        return out
    }

    private static func successor(_ label: String) -> String? {
        if let n = Int(label) { return String(n + 1) }
        guard let s = label.unicodeScalars.first, s.value < 122, let next = Unicode.Scalar(s.value + 1) else { return nil }
        return String(Character(next))
    }

    private static func codeSpans(_ text: String) -> [Range<String.Index>] {
        text.matches(of: /`[^`]*`/).map(\.range)
    }
}

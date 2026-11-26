import Foundation

/// One block of a message's Markdown. Inline Markdown (bold, code spans, links) stays inside the text and is read by
/// the app; anything this parser does not recognise stays a paragraph with its text unchanged.
public enum MarkdownBlock: Equatable, Sendable {
    public enum Align: Equatable, Sendable { case leading, center, trailing }

    public struct Item: Equatable, Sendable {
        /// The number of an ordered item as written; nil for a bullet.
        public var ordinal: Int?
        /// 0, or 1 for an item nested under one.
        public var depth: Int
        public var text: String

        public init(ordinal: Int? = nil, depth: Int = 0, text: String) {
            self.ordinal = ordinal
            self.depth = depth
            self.text = text
        }
    }

    case paragraph(String)
    case code(language: String?, text: String)
    case table(header: [String], alignments: [Align], rows: [[String]])
    case list([Item])
    /// Levels 1 to 3; deeper headings count as 3.
    case heading(level: Int, text: String)
    case quote(String)
    case rule
}

public enum MarkdownBlocks {
    public static func parse(_ text: String) -> [MarkdownBlock] {
        var p = Parser(lines: text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init))
        return p.run()
    }

    /// Whether the message is paragraphs only, so it needs no block layout.
    public static func isPlain(_ blocks: [MarkdownBlock]) -> Bool {
        blocks.allSatisfy { if case .paragraph = $0 { true } else { false } }
    }
}

/// Parsed blocks by message text, so a redraw doesn't parse again.
@MainActor
public enum MarkdownCache {
    private static var store: [String: [MarkdownBlock]] = [:]
    private static let limit = 256

    public static func blocks(_ text: String) -> [MarkdownBlock] {
        if let hit = store[text] { return hit }
        let parsed = MarkdownBlocks.parse(text)
        if store.count >= limit { store.removeAll() }
        store[text] = parsed
        return parsed
    }
}

private struct Parser {
    var lines: [String]
    var i = 0
    var out: [MarkdownBlock] = []

    init(lines: [String]) { self.lines = lines }

    mutating func run() -> [MarkdownBlock] {
        while i < lines.count {
            let line = lines[i]
            if line.trimmingCharacters(in: .whitespaces).isEmpty { i += 1; continue }
            if let block = fence() ?? heading() ?? rule() ?? table() ?? quote() ?? list() {
                out.append(block)
            } else {
                paragraph()
            }
        }
        return out
    }

    private func trimmed(_ s: String) -> String { s.trimmingCharacters(in: .whitespaces) }

    private func indent(_ s: String) -> Int {
        var n = 0
        for c in s {
            if c == " " { n += 1 } else if c == "\t" { n += 4 } else { break }
        }
        return n
    }

    private func openingFence(_ s: String) -> (mark: Character, count: Int, info: String)? {
        let t = trimmed(s)
        guard let c = t.first, c == "`" || c == "~" else { return nil }
        let count = t.prefix { $0 == c }.count
        guard count >= 3 else { return nil }
        let info = trimmed(String(t.dropFirst(count)))
        if c == "`", info.contains("`") { return nil }
        return (c, count, info)
    }

    private mutating func fence() -> MarkdownBlock? {
        guard let open = openingFence(lines[i]) else { return nil }
        let strip = indent(lines[i])
        i += 1
        var body: [String] = []
        var closed = false
        while i < lines.count {
            let t = trimmed(lines[i])
            i += 1
            if t.count >= open.count, t.allSatisfy({ $0 == open.mark }) { closed = true; break }
            body.append(String(lines[i - 1].dropFirst(min(strip, indent(lines[i - 1])))))
        }
        if !closed { while body.last.map({ trimmed($0).isEmpty }) == true { body.removeLast() } }
        let language = open.info.split(separator: " ").first.map(String.init)
        return .code(language: language, text: body.joined(separator: "\n"))
    }

    private func headingParts(_ s: String) -> (level: Int, text: String)? {
        guard s.first == "#" else { return nil }
        let hashes = s.prefix { $0 == "#" }.count
        guard hashes <= 6 else { return nil }
        let rest = s.dropFirst(hashes)
        guard rest.first == " " || rest.first == "\t" else { return nil }
        var text = trimmed(String(rest))
        while text.hasSuffix("#") { text.removeLast() }
        text = trimmed(text)
        guard !text.isEmpty else { return nil }
        return (min(hashes, 3), text)
    }

    private mutating func heading() -> MarkdownBlock? {
        guard let h = headingParts(lines[i]) else { return nil }
        i += 1
        return .heading(level: h.level, text: h.text)
    }

    private func isRule(_ s: String) -> Bool {
        let t = trimmed(s).filter { $0 != " " }
        guard t.count >= 3, let c = t.first, c == "-" || c == "*" || c == "_" else { return false }
        return t.allSatisfy { $0 == c }
    }

    private mutating func rule() -> MarkdownBlock? {
        guard isRule(lines[i]) else { return nil }
        i += 1
        return .rule
    }

    private func cells(_ s: String) -> [String] {
        var t = trimmed(s).replacingOccurrences(of: "\\|", with: "\u{1}")
        if t.hasPrefix("|") { t.removeFirst() }
        if t.hasSuffix("|") { t.removeLast() }
        return t.split(separator: "|", omittingEmptySubsequences: false).map {
            trimmed($0.replacingOccurrences(of: "\u{1}", with: "|"))
        }
    }

    private func alignments(_ s: String) -> [MarkdownBlock.Align]? {
        guard s.contains("-") else { return nil }
        let cs = cells(s)
        var out: [MarkdownBlock.Align] = []
        for c in cs {
            let core = c.trimmingCharacters(in: CharacterSet(charactersIn: ":"))
            guard !core.isEmpty, core.allSatisfy({ $0 == "-" }) else { return nil }
            switch (c.hasPrefix(":"), c.hasSuffix(":")) {
            case (true, true): out.append(.center)
            case (false, true): out.append(.trailing)
            default: out.append(.leading)
            }
        }
        return out
    }

    private func startsTable(at k: Int) -> Bool {
        guard k + 1 < lines.count, lines[k].contains("|"), let a = alignments(lines[k + 1]) else { return false }
        return a.count == cells(lines[k]).count
    }

    private mutating func table() -> MarkdownBlock? {
        guard startsTable(at: i) else { return nil }
        let header = cells(lines[i])
        let aligns = alignments(lines[i + 1]) ?? []
        i += 2
        var rows: [[String]] = []
        while i < lines.count, !trimmed(lines[i]).isEmpty, lines[i].contains("|") {
            var row = cells(lines[i])
            if row.count > header.count { row = Array(row.prefix(header.count)) }
            while row.count < header.count { row.append("") }
            rows.append(row)
            i += 1
        }
        return .table(header: header, alignments: aligns, rows: rows)
    }

    private func isQuote(_ s: String) -> Bool { trimmed(s).hasPrefix(">") }

    private mutating func quote() -> MarkdownBlock? {
        guard isQuote(lines[i]) else { return nil }
        var body: [String] = []
        while i < lines.count, isQuote(lines[i]) {
            var t = trimmed(lines[i])
            t.removeFirst()
            if t.first == " " { t.removeFirst() }
            body.append(t)
            i += 1
        }
        return .quote(body.joined(separator: "\n"))
    }

    private func marker(_ s: String) -> (ordinal: Int?, text: String)? {
        let t = trimmed(s)
        if let c = t.first, c == "-" || c == "*" || c == "+" {
            let rest = t.dropFirst()
            guard rest.first == " " || rest.first == "\t" else { return nil }
            return (nil, trimmed(String(rest)))
        }
        let digits = t.prefix { $0.isASCII && $0.isNumber }
        guard !digits.isEmpty, digits.count <= 9, let n = Int(digits) else { return nil }
        let rest = t.dropFirst(digits.count)
        guard let d = rest.first, d == "." || d == ")" else { return nil }
        let after = rest.dropFirst()
        guard after.first == " " || after.first == "\t" else { return nil }
        return (n, trimmed(String(after)))
    }

    private mutating func list() -> MarkdownBlock? {
        guard marker(lines[i]) != nil, !isRule(lines[i]) else { return nil }
        var items: [MarkdownBlock.Item] = []
        while i < lines.count {
            let line = lines[i]
            if let m = marker(line), !isRule(line) {
                if let first = items.first, (first.ordinal == nil) != (m.ordinal == nil), indent(line) < 2 { break }
                items.append(.init(ordinal: m.ordinal, depth: items.isEmpty || indent(line) < 2 ? 0 : 1, text: m.text))
                i += 1
            } else if !trimmed(line).isEmpty, indent(line) >= 2, !items.isEmpty, openingFence(line) == nil {
                items[items.count - 1].text += "\n" + trimmed(line)
                i += 1
            } else if trimmed(line).isEmpty {
                var k = i
                while k < lines.count, trimmed(lines[k]).isEmpty { k += 1 }
                guard k < lines.count, marker(lines[k]) != nil, !isRule(lines[k]) else { break }
                i = k
            } else {
                break
            }
        }
        return .list(items)
    }

    private func interrupts(at k: Int) -> Bool {
        let s = lines[k]
        if openingFence(s) != nil || headingParts(s) != nil || isRule(s) || isQuote(s) || startsTable(at: k) { return true }
        if let m = marker(s) { return m.ordinal == nil || m.ordinal == 1 }
        return false
    }

    private mutating func paragraph() {
        var body = [lines[i]]
        i += 1
        while i < lines.count, !trimmed(lines[i]).isEmpty, !interrupts(at: i) {
            body.append(lines[i])
            i += 1
        }
        out.append(.paragraph(body.joined(separator: "\n")))
    }
}

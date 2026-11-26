import Foundation

/// One Server-Sent Events message: its `id:` and its `data:` lines joined by newlines.
public struct SSEMessage: Equatable, Sendable {
    public var id: String?
    public var data: String
}

/// Turns SSE lines into messages. A blank line ends a message; comment lines (`:` first) are keep-alives.
public struct SSEParser: Sendable {
    private var data: [String] = []
    private var id: String?

    public init() {}

    public mutating func feed(line: String) -> SSEMessage? {
        if line.isEmpty {
            defer { data = []; id = nil }
            guard !data.isEmpty else { return nil }
            return SSEMessage(id: id, data: data.joined(separator: "\n"))
        }
        if line.hasPrefix(":") { return nil }
        let field: Substring
        var value: Substring
        if let colon = line.firstIndex(of: ":") {
            field = line[..<colon]
            value = line[line.index(after: colon)...]
            if value.hasPrefix(" ") { value = value.dropFirst() }
        } else {
            field = Substring(line)
            value = ""
        }
        switch field {
        case "data": data.append(String(value))
        case "id": id = String(value)
        default: break
        }
        return nil
    }
}

/// Splits a byte stream into lines on LF, dropping a trailing CR, and keeps empty lines (they end SSE messages).
public struct LineSplitter: Sendable {
    private var buffer: [UInt8] = []

    public init() {}

    public mutating func feed(_ byte: UInt8) -> String? {
        guard byte == UInt8(ascii: "\n") else {
            buffer.append(byte)
            return nil
        }
        if buffer.last == UInt8(ascii: "\r") { buffer.removeLast() }
        defer { buffer.removeAll(keepingCapacity: true) }
        return String(decoding: buffer, as: UTF8.self)
    }
}

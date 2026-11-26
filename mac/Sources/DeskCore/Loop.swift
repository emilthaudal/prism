import Foundation

/// An overnight loop on a session: it runs turn after turn until its time is up, pausing when usage is over budget.
public struct Loop: Codable, Equatable, Sendable {
    public var turn: Int
    public var turns: Int
    public var until: Date?
    /// `running`, `paused` or `ended`.
    public var state: String
    public var pausedUntil: Date?

    public init(turn: Int, turns: Int, until: Date? = nil, state: String = "running", pausedUntil: Date? = nil) {
        self.turn = turn
        self.turns = turns
        self.until = until
        self.state = state
        self.pausedUntil = pausedUntil
    }

    public var isPaused: Bool { state == "paused" }
    public var isLive: Bool { state == "running" || isPaused }
    /// How far along the loop is by turns, 0 to 1.
    public var fraction: Double { turns > 0 ? min(max(Double(turn) / Double(turns), 0), 1) : 0 }
    public var iteration: String { "iteration \(turn) of \(turns)" }

    /// "loop 3/20 · until 07:00", or "loop paused until 16:40"; times are local, 24 hour.
    public func label(timeZone: TimeZone = .current) -> String {
        if isPaused { return "loop paused" + (pausedUntil.map { " until " + Self.clock($0, timeZone) } ?? "") }
        if state == "ended" { return "loop ended" }
        return "loop \(turn)/\(turns)" + (until.map { " · until " + Self.clock($0, timeZone) } ?? "")
    }

    /// The board card's last line: "until 07:00", or "paused until 16:40 · usage over budget".
    public func footer(timeZone: TimeZone = .current) -> String? {
        if isPaused { return "paused" + (pausedUntil.map { " until " + Self.clock($0, timeZone) } ?? "") + " · usage over budget" }
        return until.map { "until " + Self.clock($0, timeZone) }
    }

    private static func clock(_ date: Date, _ timeZone: TimeZone) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = timeZone
        f.dateFormat = "HH:mm"
        return f.string(from: date)
    }
}

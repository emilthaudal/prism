import Foundation

/// The Claude usage limits in the snapshot's `desk.usage` (docs/serve-api.md): what the sidebar foot shows.
public struct Usage: Codable, Equatable, Sendable {
    public var fiveHour: UsageLimit?
    public var sevenDay: UsageLimit?
    public var updatedAt: Date?

    public init(fiveHour: UsageLimit? = nil, sevenDay: UsageLimit? = nil, updatedAt: Date? = nil) {
        self.fiveHour = fiveHour
        self.sevenDay = sevenDay
        self.updatedAt = updatedAt
    }

    /// Older than this, the numbers are dimmed and show their age: no Claude session has drawn its status line since.
    public static let staleAfter: TimeInterval = 10 * 60

    public func isStale(now: Date) -> Bool {
        guard let at = updatedAt else { return false }
        return now.timeIntervalSince(at) > Self.staleAfter
    }
}

public struct UsageLimit: Codable, Equatable, Sendable {
    public var usedPercentage: Double
    public var resetsAt: Date?

    public init(usedPercentage: Double, resetsAt: Date? = nil) {
        self.usedPercentage = usedPercentage
        self.resetsAt = resetsAt
    }

    public enum Level: Equatable, Sendable {
        /// Below 75 %.
        case plenty
        /// From 75 %.
        case close
        /// From 90 %.
        case nearlyOut
        /// At 100 %, before it resets.
        case reached
    }

    /// Past its reset time the limit has started over, whatever the last number said.
    public func level(now: Date) -> Level {
        if let r = resetsAt, r <= now { return .plenty }
        switch usedPercentage {
        case 100...: return .reached
        case 90...: return .nearlyOut
        case 75...: return .close
        default: return .plenty
        }
    }

    /// The percentage to show, 0 once the limit has reset.
    public func shownPercentage(now: Date) -> Int {
        if let r = resetsAt, r <= now { return 0 }
        return Int(min(100, max(0, usedPercentage)).rounded())
    }
}

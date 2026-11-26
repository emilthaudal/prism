import Foundation

/// Where "last looked" is kept between launches.
public protocol LastLookedStore {
    func read() -> Date?
    func write(_ date: Date)
}

public struct UserDefaultsLastLooked: LastLookedStore {
    private static let key = "lastLookedAt"
    nonisolated(unsafe) private let defaults: UserDefaults

    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    public func read() -> Date? { defaults.object(forKey: Self.key) as? Date }
    public func write(_ date: Date) { defaults.set(date, forKey: Self.key) }
}

/// One line of the "While you were away" timeline.
public struct AwayEntry: Equatable, Sendable, Identifiable {
    public var id: String
    public var at: Date
    public var title: String
    /// The dim second line.
    public var detail: String?
    public var changes: Changes?
    /// What a click selects: a session, or a track.
    public var session: String?
    public var track: String?
}

public struct AwayRow: Equatable, Sendable {
    public var title: String
    public var trailing: String?
}

/// What happened since Emil last had the window in front, from the snapshot's timestamps alone.
public struct AwayDigest: Equatable, Sendable {
    public var since: Date
    public var away: TimeInterval
    public var finished = 0
    public var landed = 0
    public var ready = 0
    public var handled = 0
    public var takenBack = 0
    public var loopsEnded = 0
    public var decisions = 0
    /// Newest first, all of it; the board shows the first `Away.shown`.
    public var timeline: [AwayEntry] = []
    public var timeZone: TimeZone = .current

    public var isEmpty: Bool { rows.isEmpty && timeline.isEmpty }
    public var shown: [AwayEntry] { Array(timeline.prefix(Away.shown)) }
    public var hidden: Int { max(0, timeline.count - Away.shown) }

    /// "Since 23:10".
    public var heading: String { "Since " + Away.clock(since, timeZone) }
    /// "8 h 20 m away".
    public var awayLine: String { Away.duration(away) + " away" }

    /// The summary card's rows; a row with nothing to say is left out.
    public var rows: [AwayRow] {
        var out: [AwayRow] = []
        if finished > 0 {
            let parts = [(landed, "landed"), (ready, "ready")].filter { $0.0 > 0 }.map { "\($0.0) \($0.1)" }
            out.append(AwayRow(title: "\(finished) session\(finished == 1 ? "" : "s") finished", trailing: parts.isEmpty ? nil : parts.joined(separator: " · ")))
        }
        if handled > 0 {
            out.append(AwayRow(title: "Prism handled \(handled) thing\(handled == 1 ? "" : "s") itself", trailing: takenBack > 0 ? "\(takenBack) taken back" : nil))
        }
        if loopsEnded > 0 { out.append(AwayRow(title: "\(loopsEnded) loop\(loopsEnded == 1 ? "" : "s") ended", trailing: nil)) }
        if decisions > 0 { out.append(AwayRow(title: "\(decisions) decision\(decisions == 1 ? "" : "s") waiting", trailing: nil)) }
        return out
    }
}

public enum Away {
    /// The least time away that earns a digest.
    public static let minimum: TimeInterval = 2 * 3600
    /// How long the window must have been in front for the digest to count as read.
    public static let readAfter: TimeInterval = 60
    public static let shown = 8

    /// The digest for a window coming back after `lastLooked`, or nil when it was away less than `minimum`, or nothing happened.
    public static func digest(lastLooked: Date?, now: Date, snapshot s: Snapshot, timeZone: TimeZone = .current) -> AwayDigest? {
        guard let last = lastLooked, now.timeIntervalSince(last) >= minimum else { return nil }
        var d = AwayDigest(since: last, away: now.timeIntervalSince(last))
        d.timeZone = timeZone
        func after(_ date: Date?) -> Bool { date.map { $0 > last } ?? false }
        var entries: [AwayEntry] = []

        for session in s.sessions where session.id != s.desk.session && after(session.updatedAt) {
            guard let at = session.updatedAt else { continue }
            if session.state == .done {
                d.finished += 1
                let verb: String
                if session.isLanded {
                    d.landed += 1
                    verb = "landed"
                } else if session.changes != nil {
                    d.ready += 1
                    verb = "ready to land"
                } else {
                    verb = "finished"
                }
                entries.append(AwayEntry(id: "s:" + session.id, at: at, title: "\(session.displayName) \(verb)", detail: session.lastAnswerGist, changes: session.changes, session: session.id))
            } else if session.state == .error {
                entries.append(AwayEntry(id: "s:" + session.id, at: at, title: "\(session.displayName) stopped with an error", detail: session.lastAnswerGist, changes: session.changes, session: session.id))
            }
            if session.loop?.state == "ended" { d.loopsEnded += 1 }
        }

        for entry in s.handled where entry.kind != "answered" && after(entry.at) {
            guard let at = entry.at else { continue }
            d.handled += 1
            if entry.undone == true { d.takenBack += 1 }
            let name = handledName(entry, in: s)
            let kind = entry.kind.flatMap { $0.isEmpty ? nil : $0 }
            let isTrack = entry.track != nil && entry.track != "desk" && s.track(entry.track) != nil
            entries.append(AwayEntry(id: "h:" + entry.id, at: at, title: [name, kind].compactMap { $0 }.joined(separator: " "), detail: entry.did.flatMap { $0.isEmpty ? nil : $0 }, track: isTrack ? entry.track : nil))
        }

        d.decisions = s.openQuestions.filter { after($0.createdAt) }.count
        d.timeline = entries.sorted { $0.at != $1.at ? $0.at > $1.at : $0.id < $1.id }
        return d.isEmpty ? nil : d
    }

    /// The track's name, or "Prism" for what the desk noted for itself.
    public static func handledName(_ entry: Handled, in s: Snapshot) -> String {
        if entry.track == "desk" { return "Prism" }
        return s.track(entry.track)?.name ?? entry.track ?? ""
    }

    /// 24-hour local time, "23:10".
    public static func clock(_ date: Date, _ timeZone: TimeZone = .current) -> String {
        let c = Calendar(identifier: .gregorian)
        var cal = c
        cal.timeZone = timeZone
        let parts = cal.dateComponents([.hour, .minute], from: date)
        return String(format: "%02d:%02d", parts.hour ?? 0, parts.minute ?? 0)
    }

    /// "8 h 20 m", "8 h", "2 d 3 h".
    public static func duration(_ seconds: TimeInterval) -> String {
        let minutes = Int(seconds / 60)
        let (d, h, m) = (minutes / 1440, minutes % 1440 / 60, minutes % 60)
        if d > 0 { return h > 0 ? "\(d) d \(h) h" : "\(d) d" }
        if h > 0 { return m > 0 ? "\(h) h \(m) m" : "\(h) h" }
        return "\(m) m"
    }
}

/// When Emil last had the window in front, and whether the digest of what he missed is up. A launch or fixture run
/// never writes the store: only a store handed in does, and the app hands none to a fixture run or an `--away-since` run.
public struct AwayWatch {
    private let store: (any LastLookedStore)?
    public private(set) var lastLooked: Date?
    /// While the digest is up: when Emil last looked before he went away.
    public private(set) var since: Date?
    private var frontSince: Date?

    public init(store: (any LastLookedStore)? = nil, awaySince: Date? = nil) {
        self.store = store
        lastLooked = awaySince ?? store?.read()
    }

    /// The watch for a run: the user defaults for a real run, nothing for a fixture or an `--away-since` run.
    public static func forRun(fixture: Bool, awaySince: Date?, store: any LastLookedStore = UserDefaultsLastLooked()) -> AwayWatch {
        fixture || awaySince != nil ? AwayWatch(awaySince: awaySince) : AwayWatch(store: store)
    }

    public var showing: Bool { since != nil }

    /// The window came in front (or the app launched): after `Away.minimum` away, the digest goes up.
    public mutating func windowBecameKey(now: Date) {
        frontSince = now
        guard since == nil, let last = lastLooked, now.timeIntervalSince(last) >= Away.minimum else { return }
        since = last
    }

    /// The window lost key, the app deactivated or it quit. A digest that was in front for `Away.readAfter` goes away.
    public mutating func windowResigned(now: Date) {
        if let front = frontSince, since != nil, now.timeIntervalSince(front) >= Away.readAfter { since = nil }
        frontSince = nil
        looked(now)
    }

    /// "Got it".
    public mutating func dismiss(now: Date) {
        since = nil
        looked(now)
    }

    private mutating func looked(_ now: Date) {
        lastLooked = now
        store?.write(now)
    }
}

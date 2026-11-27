import Foundation

/// A link on a track's card: where it goes and a short title for its button.
public struct BriefLink: Equatable, Sendable, Identifiable {
    public var url: URL
    /// The host and the last path part: "github.com #418", "circleci.com pipelines", "github.com" for a bare host.
    public var title: String
    public var id: String { url.absoluteString }

    init?(_ text: String) {
        guard let url = URL(string: text.trimmingCharacters(in: .whitespaces)), url.scheme == "https" || url.scheme == "http", let host = url.host() else { return nil }
        self.url = url
        let last = url.pathComponents.last { $0 != "/" }
        let part = last.map { Int($0) != nil ? "#" + $0 : $0 }
        title = [host, part].compactMap { $0 }.joined(separator: " ")
    }
}

/// One open track on the quiet board: what it is for, where it stands and what it waits on.
public struct TrackBrief: Equatable, Sendable, Identifiable {
    public var track: Track
    /// The track's chip as the sidebar shows it folded.
    public var chip: StatusChip
    public var goal: String?
    /// What the track says it is doing, else its session's live activity, else its last answer.
    public var now: String?
    public var today: String?
    public var check: TrackCheck?
    /// Who the track waits on, if anybody.
    public var person: String?
    public var links: [BriefLink]
    public var id: String { track.id }
    /// Whether the card says anything beyond the track's state: a goal, today's plan, a check or links.
    public var hasPlan: Bool { goal != nil || today != nil || check != nil || !links.isEmpty }
}

/// One live overnight loop on the board.
public struct LoopBrief: Equatable, Sendable, Identifiable {
    public var session: Session
    public var loop: Loop
    public var track: Track?
    public var id: String { session.id }
    public var name: String { track?.name ?? session.displayName }
    public var goal: String? { track?.goal.flatMap(BoardPlan.text) }
    /// What the session does now, else what it last said.
    public var now: String? { session.liveActivity.map(Activity.readable) ?? session.lastAnswerGist }

    /// Every session whose loop is running or paused, in the snapshot's order.
    public static func all(_ s: Snapshot) -> [LoopBrief] {
        s.sessions.compactMap { session in
            guard let loop = session.loop, loop.isLive else { return nil }
            return LoopBrief(session: session, loop: loop, track: s.track(session.track))
        }
    }
}

/// A waiting track as one compact row.
public struct WaitingBrief: Equatable, Sendable, Identifiable {
    public var track: Track
    /// What it waits on, as the sidebar's second line says it.
    public var detail: String?
    public var id: String { track.id }
}

/// The board's quiet state: while nothing needs Emil, a digest of the day and a brief per open track. Empty while a
/// question is open.
public struct BoardPlan: Equatable, Sendable {
    public var isQuiet: Bool
    /// "3 landed", "5 handled by Prism", "2 answered": parts that are zero are left out.
    public var digest: [String]
    /// Focus tracks, then background tracks, each by rank, as the sidebar orders them.
    public var briefs: [TrackBrief]
    public var waiting: [WaitingBrief]

    public var digestLine: String? { digest.isEmpty ? nil : "Today: " + digest.joined(separator: " · ") }
    public var tracksCount: Int { briefs.count + waiting.count }

    /// The handled entries of today's calendar day.
    public static func handledToday(_ entries: [Handled], now: Date) -> [Handled] {
        entries.filter { $0.at.map { Calendar.current.isDate($0, inSameDayAs: now) } ?? false }
    }

    public init(_ s: Snapshot, now: Date) {
        let board = Board(s)
        isQuiet = board.needsYou.isEmpty
        guard isQuiet else {
            digest = []
            briefs = []
            waiting = []
            return
        }
        let today = Self.handledToday(s.handled, now: now)
        let answered = today.filter { $0.kind == "answered" }.count
        var parts: [(Int, String)] = [(board.landed.count, "landed"), (today.count - answered, "handled by Prism"), (answered, "answered")]
        parts.removeAll { $0.0 == 0 }
        digest = parts.map { "\($0.0) \($0.1)" }
        let rows = Sidebar(s).sections
        // A track with a live loop has its card under Loops already.
        let looping = Set(LoopBrief.all(s).compactMap { $0.track?.id })
        briefs = rows.filter { ["focus", "background"].contains($0.mode) }.flatMap(\.tracks).filter { !looping.contains($0.track.id) }.map(Self.brief)
        waiting = rows.filter { $0.mode == "waiting" }.flatMap(\.tracks).map { WaitingBrief(track: $0.track, detail: $0.line ?? $0.track.blocker) }
    }

    static func brief(_ row: SidebarTrack) -> TrackBrief {
        TrackBrief(
            track: row.track,
            chip: row.chip,
            goal: text(row.track.goal),
            now: text(row.track.where) ?? row.primary?.liveActivity.map(Activity.readable) ?? row.primary?.lastAnswerGist,
            today: text(row.track.today),
            check: row.track.check,
            person: row.track.waitingOnPerson,
            links: (row.track.links ?? []).compactMap(BriefLink.init)
        )
    }

    /// A brief per focus, background and waiting track without a live loop, whatever the board's state.
    public static func openBriefs(_ s: Snapshot) -> [TrackBrief] {
        let looping = Set(LoopBrief.all(s).compactMap { $0.track?.id })
        return Sidebar(s).sections.filter { ["focus", "background", "waiting"].contains($0.mode) }.flatMap(\.tracks).filter { !looping.contains($0.track.id) }.map(brief)
    }

    static func text(_ s: String?) -> String? {
        guard let t = s?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { return nil }
        return t
    }
}

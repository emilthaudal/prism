import Foundation

/// The board's four groups, worked out from a snapshot.
public struct Board: Equatable, Sendable {
    public var needsYou: [Question]
    /// Answers recorded while the agent was busy, until they are typed into it.
    public var queued: [Question]
    public var inFlight: [Session]
    /// Finished sessions Emil hasn't opened or dismissed: each keeps its outcome, last answer and the git facts.
    public var reports: [Session]
    public var readyToLand: [Session]
    public var waitingOnYou: Int
    public var landed: [Session]

    public init(_ s: Snapshot) {
        let rank = Dictionary(s.tracks.map { ($0.id, $0.rank ?? Int.max) }, uniquingKeysWith: { a, _ in a })
        // A dialog's questions sit together, where its oldest question would, in merge, install, close order.
        var first: [String: Date] = [:]
        for q in s.openQuestions { first[q.dialog] = min(first[q.dialog] ?? .distantFuture, q.createdAt ?? .distantPast) }
        needsYou = s.openQuestions.sorted {
            let (ra, rb) = (rank[$0.track ?? ""] ?? Int.max, rank[$1.track ?? ""] ?? Int.max)
            if ra != rb { return ra < rb }
            if $0.dialog != $1.dialog {
                let (fa, fb) = (first[$0.dialog] ?? .distantPast, first[$1.dialog] ?? .distantPast)
                return fa != fb ? fa < fb : $0.dialog < $1.dialog
            }
            if $0.dialogOrder != $1.dialogOrder { return $0.dialogOrder < $1.dialogOrder }
            return ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast)
        }
        queued = s.queuedQuestions.sorted { ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast) }
        let work = s.sessions.filter { $0.id != s.desk.session }
        inFlight = work.filter { [.working, .blocked, .error].contains($0.state) || ($0.state == .idle && !$0.isEmils) }
        reports = work.filter(\.isUnseenDone).sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
        readyToLand = work.filter { $0.state == .done && $0.changes != nil && !$0.isLanded && !$0.isUnseenDone }
        waitingOnYou = s.waitingOnYou
        landed = work.filter(\.isLanded)
    }

    /// Where each question and session sits, by id. A change in it is a card arriving, leaving or moving section.
    public var placements: [String: String] {
        var out: [String: String] = [:]
        for q in needsYou { out[q.id] = "needsYou" }
        for q in queued { out[q.id] = "queued" }
        for s in reports { out[s.id] = "reports" }
        for s in inFlight { out[s.id] = "inFlight" }
        for s in readyToLand { out[s.id] = "readyToLand" }
        for s in landed { out[s.id] = "landed" }
        return out
    }

    public var summary: String {
        var parts: [String] = []
        if waitingOnYou > 0 { parts.append("\(waitingOnYou) waiting on you") }
        let running = inFlight.filter { $0.state == .working }.count
        if running > 0 { parts.append("\(running) running") }
        if !readyToLand.isEmpty { parts.append("\(readyToLand.count) ready") }
        return parts.isEmpty ? "All quiet" : parts.joined(separator: " · ")
    }
}

/// What a row in the sidebar shows at its trailing edge.
public enum StatusChip: Equatable, Sendable {
    case question
    case permission
    case error
    case working
    case doneUnseen
    case done
    case idle
    case stopped
    case waitingOn(String, since: Date?)
    case sessions(Int)
    case none
}

/// A session's row in the sidebar, with its subagents nested one level further.
public struct SidebarSession: Equatable, Sendable, Identifiable {
    public var session: Session
    public var chip: StatusChip
    /// The dim second line, while the session works.
    public var activity: String?
    public var subagents: [Subagent]
    /// "yours" for Emil's own session, "desk" for one the desk runs.
    public var owner: String
    public var id: String { session.id }

    /// The row's name under its track, without repeating the track's name: a session named after its track shows
    /// what it does (its activity), else what it last said, else "Session"; one named "TRACK-rest" shows the rest.
    public func title(under track: String?) -> String {
        let name = session.displayName
        guard let track, !track.isEmpty else { return name }
        if name == track { return activity ?? session.lastAnswerGist ?? "Session" }
        for sep in ["-", "_", " ", "/"] where name.hasPrefix(track + sep) {
            let rest = name.dropFirst(track.count + sep.count)
            if rest.count >= 3 { return String(rest) }
        }
        return name
    }

    /// The dim second line, unless the activity already is the row's name.
    public func activityLine(under track: String?) -> String? {
        let shown = activity.flatMap { title(under: track) == $0 ? nil : $0 }
        return session.loopLead(shown)
    }
}

public struct SidebarTrack: Equatable, Sendable, Identifiable {
    public var track: Track
    /// The sessions listed under the track: only when two or more are live, else none.
    public var nested: [SidebarSession]
    /// The dim line under the track's name, when its sessions aren't listed.
    public var line: String? = nil
    /// The session the track row opens: the one live session, or the newest.
    public var primary: Session?
    /// The track's chip when its sessions are hidden.
    public var chip: StatusChip
    public var ownedByEmil: Bool
    public var id: String { track.id }

    /// Parked tracks start folded.
    public var startsCollapsed: Bool { track.mode == "parked" }

    /// The chip the track row shows: with its sessions listed, a session count (what needs Emil is
    /// on their rows); folded, the whole track's chip.
    public func chip(expanded: Bool) -> StatusChip {
        guard expanded, !nested.isEmpty else { return chip }
        switch chip {
        case .waitingOn: return chip
        default: return .sessions(nested.count)
        }
    }
}

/// Which tracks Emil folded or unfolded; the rest follow their default.
public struct SidebarFolds: Equatable, Sendable {
    public var overrides: [String: Bool] = [:]

    public init() {}

    public func isExpanded(_ row: SidebarTrack) -> Bool { overrides[row.id] ?? !row.startsCollapsed }

    public mutating func toggle(_ row: SidebarTrack) { overrides[row.id] = !isExpanded(row) }
}

public struct SidebarSection: Equatable, Sendable, Identifiable {
    public var mode: String
    public var title: String
    public var tracks: [SidebarTrack]
    public var id: String { mode }
}

/// The sidebar: the desk first, then tracks by mode with the desk's sessions nested under their track.
public struct Sidebar: Equatable, Sendable {
    public static let modes: [(mode: String, title: String)] = [("focus", "Focus"), ("background", "Background"), ("waiting", "Waiting"), ("parked", "Parked")]

    public var sections: [SidebarSection]
    /// The desk's sessions that belong to no track.
    public var deskSessions: [SidebarSession]
    /// The header's one count: session questions, desk decisions and blocked sessions together.
    public var waitingOnYou: Int

    public init(_ s: Snapshot) {
        waitingOnYou = s.waitingOnYou
        let open = s.openQuestions
        let work = s.sessions.filter { $0.id != s.desk.session && !$0.isLanded }
        let known = Set(s.tracks.map(\.id))
        // A desk-run session with no track shows only while it works or is blocked; "desk · stopped" under the Prism row
        // said the same as the row, in a more worrying word.
        deskSessions = work.filter { ($0.track == nil || !known.contains($0.track!)) && !(!$0.isEmils && [.stopped, .idle].contains($0.state)) }.map { Self.sessionRow($0, questions: s.questions) }
        let byMode = Dictionary(grouping: s.tracks, by: \.shown)
        let listed = Set(Self.modes.map(\.mode))
        var modes = Self.modes
        for extra in byMode.keys.sorted() where !listed.contains(extra) { modes.append((extra, extra.capitalized)) }
        sections = modes.compactMap { mode, title in
            guard let tracks = byMode[mode], !tracks.isEmpty else { return nil }
            let rows = tracks.sorted { ($0.rank ?? Int.max, $0.name) < ($1.rank ?? Int.max, $1.name) }.map { t in
                Self.row(t, sessions: work.filter { $0.track == t.id }.sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }, questions: open.filter { $0.track == t.id }, all: s.questions)
            }
            return SidebarSection(mode: mode, title: title, tracks: rows)
        }
    }

    /// The ordered ids of the sections, tracks and sessions the list shows. Activity text and chips are not in it.
    public var structure: [String] {
        deskSessions.map(\.id) + sections.flatMap { [$0.mode] + $0.tracks.flatMap { [$0.id] + $0.nested.map(\.id) } }
    }

    static func sessionRow(_ s: Session, questions: [Question]) -> SidebarSession {
        SidebarSession(session: s, chip: chip(for: s, questions: questions), activity: s.liveActivity.map(Activity.readable), subagents: s.subagents ?? [], owner: s.isEmils ? "yours" : "desk")
    }

    /// A track's sessions as the sidebar lists them, newest first: a stopped session shows only while it is the
    /// newest, so earlier runs of a track don't pile up under it.
    static func listed(_ sessions: [Session]) -> [Session] {
        sessions.enumerated().filter { i, s in i == 0 || s.state != .stopped }.map(\.element)
    }

    static func row(_ t: Track, sessions: [Session], questions: [Question], all: [Question]) -> SidebarTrack {
        let primary = sessions.first { [.working, .blocked].contains($0.state) } ?? sessions.first
        let ownedByEmil = primary?.isEmils ?? true
        let chip: StatusChip
        if !questions.isEmpty {
            chip = sessions.contains(where: \.waitsOnPermission) ? .permission : .question
        } else if sessions.contains(where: { $0.state == .error }) {
            chip = .error
        } else if let person = t.blocker, sessions.allSatisfy({ $0.state != .working }) {
            chip = .waitingOn(person, since: t.updatedAt)
        } else if listed(sessions).count > 1 {
            chip = .sessions(listed(sessions).count)
        } else if let p = primary {
            chip = Self.chip(for: p, questions: [])
        } else {
            chip = .none
        }
        // Compact (Emil, 26 Nov): one row per track; its sessions branch out only when two or more are live, or one
        // has subagents to show.
        let shown = listed(sessions)
        let live = shown.filter { $0.state != .stopped }
        let branches = live.count > 1 || shown.contains { !($0.subagents ?? []).isEmpty }
        var row = SidebarTrack(track: t, nested: branches ? shown.map { sessionRow($0, questions: all) } : [], primary: primary, chip: chip, ownedByEmil: ownedByEmil)
        row.line = branches ? nil : line(t, primary: primary, chip: chip)
        return row
    }

    /// The track row's second line: what its session does now, what the track waits on, its check and last
    /// answer, else what it last said.
    static func line(_ t: Track, primary: Session?, chip: StatusChip) -> String? {
        // A loop's iteration and deadline are on its board card; the row says what it does now.
        if let p = primary, p.loop?.isLive == true, let now = p.liveActivity.map(Activity.readable) ?? p.lastAnswerGist { return now }
        if let a = primary?.liveActivity { return Activity.readable(a) }
        // As written: notes start lowercase already, and a note can start with a name ("Alex").
        if case .waitingOn(let who, _) = chip { return t.check?.blocker == who ? who : "Waits on " + who }
        if let c = t.check, primary?.state != .working { return c.pickUpLine ?? "Check " + c.summary }
        return primary?.lastAnswerGist
    }

    /// A session's own chip.
    public static func chip(for s: Session, questions: [Question]) -> StatusChip {
        if questions.contains(where: { $0.session == s.id && $0.isOpen }) || s.state == .blocked {
            return s.waitsOnPermission ? .permission : .question
        }
        switch s.state {
        case .working: return .working
        case .done: return s.seenAt == nil ? .doneUnseen : .done
        case .error: return .error
        case .stopped: return .stopped
        case .idle, .blocked: return .idle
        }
    }
}

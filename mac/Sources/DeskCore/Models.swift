import Foundation

/// What `GET /v1/snapshot` returns (docs/serve-api.md). Fields a server may leave out are optional, so an older or
/// newer server still decodes.
public struct Snapshot: Codable, Equatable, Sendable {
    public var seq: Int
    public var generatedAt: Date?
    public var desk: DeskInfo
    public var tracks: [Track]
    public var sessions: [Session]
    public var questions: [Question]
    public var conversation: [ConversationEntry]
    public var handled: [Handled]

    public init(seq: Int = 0, generatedAt: Date? = nil, desk: DeskInfo = DeskInfo(), tracks: [Track] = [], sessions: [Session] = [], questions: [Question] = [], conversation: [ConversationEntry] = [], handled: [Handled] = []) {
        self.seq = seq
        self.generatedAt = generatedAt
        self.desk = desk
        self.tracks = tracks
        self.sessions = sessions
        self.questions = questions
        self.conversation = conversation
        self.handled = handled
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(Int.self, forKey: .seq)
        generatedAt = try c.decodeIfPresent(Date.self, forKey: .generatedAt)
        desk = try c.decodeIfPresent(DeskInfo.self, forKey: .desk) ?? DeskInfo()
        tracks = try c.decodeIfPresent([Track].self, forKey: .tracks) ?? []
        sessions = try c.decodeIfPresent([Session].self, forKey: .sessions) ?? []
        questions = try c.decodeIfPresent([Question].self, forKey: .questions) ?? []
        conversation = try c.decodeIfPresent([ConversationEntry].self, forKey: .conversation) ?? []
        handled = try c.decodeIfPresent([Handled].self, forKey: .handled) ?? []
    }

    public static let empty = Snapshot()

    public func track(_ id: String?) -> Track? { id.flatMap { id in tracks.first { $0.id == id } } }
    public func session(_ id: String?) -> Session? { id.flatMap { id in sessions.first { $0.id == id } } }
    public func question(_ id: String?) -> Question? { id.flatMap { id in questions.first { $0.id == id } } }
    public var openQuestions: [Question] { questions.filter(\.isOpen) }
    public var queuedQuestions: [Question] { questions.filter(\.isQueued) }

    /// Everything that waits on Emil, as one number: the open questions (a session's, and the desk's decisions about a
    /// track), plus a blocked session that has no open question of its own.
    public var waitingOnYou: Int {
        let open = openQuestions
        let asking = Set(open.compactMap(\.session))
        let blocked = sessions.filter { $0.id != desk.session && $0.state == .blocked && !asking.contains($0.id) }
        return open.count + blocked.count
    }
}

public struct DeskInfo: Codable, Equatable, Sendable {
    public var session: String?
    public var status: String?
    public var queuedEvents: Int?
    /// Emil's Claude usage limits as his status line last saw them; nil until it has written any (EMI-408).
    public var usage: Usage?

    public init(session: String? = nil, status: String? = nil, queuedEvents: Int? = nil, usage: Usage? = nil) {
        self.session = session
        self.status = status
        self.queuedEvents = queuedEvents
        self.usage = usage
    }
}

/// One step on the way to a track's goal.
public struct Milestone: Codable, Equatable, Sendable {
    public var title: String
    public var done: Bool

    public init(title: String, done: Bool = false) {
        self.title = title
        self.done = done
    }
}

public struct Track: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var goal: String?
    public var mode: String
    /// The mode the track is shown in: the server computes waiting from the track's check or
    /// waiting_on, its asks and its sessions. Nil from a server that does not send it.
    public var shownMode: String?
    public var tier: String?
    public var rank: Int?
    public var today: String?
    public var `where`: String?
    public var waitingOn: String?
    /// The track's check: line and tracks check's last answer to it (EMI-431); nil when it has none.
    public var check: TrackCheck?
    public var links: [String]?
    public var blockedBy: [String]?
    public var updatedAt: Date?
    /// The steps to the goal, in order. prism server sends them when the track's agent set any (EMI-485); the key is absent otherwise.
    public var milestones: [Milestone]?

    public init(id: String, name: String, goal: String? = nil, mode: String = "focus", shownMode: String? = nil, tier: String? = nil, rank: Int? = nil, today: String? = nil, where: String? = nil, waitingOn: String? = nil, check: TrackCheck? = nil, links: [String]? = nil, blockedBy: [String]? = nil, updatedAt: Date? = nil, milestones: [Milestone]? = nil) {
        self.id = id
        self.name = name
        self.goal = goal
        self.mode = mode
        self.shownMode = shownMode
        self.tier = tier
        self.rank = rank
        self.today = today
        self.where = `where`
        self.waitingOn = waitingOn
        self.check = check
        self.links = links
        self.blockedBy = blockedBy
        self.updatedAt = updatedAt
        self.milestones = milestones
    }

    /// The first milestone not reached yet: what the track does next.
    public var nextMilestone: Milestone? { milestones?.first { !$0.done } }
    public var milestonesDone: Int { milestones?.filter(\.done).count ?? 0 }

    /// The group the sidebar puts the track in.
    public var shown: String { shownMode ?? mode }

    /// What a waiting track waits on, structured first: its check's short label ("PR #191 review"), else the
    /// waiting_on note as written. The full note stays in `waitingOn` and the track's detail.
    public var blocker: String? {
        if shown == "waiting", let b = check?.blocker { return b }
        return waitingOnPerson
    }

    /// Who the track waits on, or nil when it waits on nobody.
    public var waitingOnPerson: String? {
        guard let w = waitingOn?.trimmingCharacters(in: .whitespaces), !w.isEmpty else { return nil }
        return w
    }
}

/// What a track waits on GitHub for, as `pr 12 approved`, and the answer tracks check last got.
public struct TrackCheck: Codable, Equatable, Sendable {
    public var text: String
    /// "yes" or "no"; nil until the check has run.
    public var answer: String?
    public var ranAt: Date?

    public init(text: String, answer: String? = nil, ranAt: Date? = nil) {
        self.text = text
        self.answer = answer
        self.ranAt = ranAt
    }

    /// What the track waits on, short enough for a sidebar row: "PR #191 review", "PR #191 merge", "PR #191 CI", "CI main".
    /// nil for a check text it doesn't know.
    public var blocker: String? {
        let w = text.split(separator: " ").map(String.init)
        if w.count == 3, w[0] == "pr", Int(w[1]) != nil {
            switch w[2] {
            case "approved": return "PR #\(w[1]) review"
            case "merged": return "PR #\(w[1]) merge"
            case "green": return "PR #\(w[1]) CI"
            default: return nil
            }
        }
        if w.count == 3, w[0] == "ci", w[2] == "green" { return "CI \(w[1])" }
        return nil
    }

    /// One line for the track's row: `pr 12 approved: yes`, or `not checked yet` before the first run.
    public var summary: String { text + ": " + (answer ?? "not checked yet") }

    /// "Picks up when PR 191 is approved": what the track waits for, in words, for a track whose card will pick up again.
    /// nil for a check text it doesn't know.
    public var pickUpLine: String? {
        let w = text.split(separator: " ").map(String.init)
        if w.count == 3, w[0] == "pr", Int(w[1]) != nil {
            switch w[2] {
            case "approved": return "Picks up when PR \(w[1]) is approved"
            case "merged": return "Picks up when PR \(w[1]) is merged"
            case "green": return "Picks up when PR \(w[1]) is green"
            default: return nil
            }
        }
        if w.count == 3, w[0] == "ci", w[2] == "green" { return "Picks up when CI on \(w[1]) is green" }
        return nil
    }
}

public enum SessionStatus: String, Sendable {
    case idle, working, done, blocked, error, stopped
}

/// A subagent a session started; done ones stay until the session's next prompt.
public struct Subagent: Codable, Equatable, Sendable {
    public var name: String
    public var status: String

    public init(name: String, status: String = "working") {
        self.name = name
        self.status = status
    }

    public var isWorking: Bool { status == "working" }
}

public struct Session: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var short: String?
    public var track: String?
    public var parent: String?
    public var owner: String
    public var host: String?
    public var name: String?
    public var status: String
    public var waitingFor: String?
    public var activity: String?
    public var subagents: [Subagent]?
    public var lastAnswer: String?
    public var changes: Changes?
    public var seenAt: Date?
    public var updatedAt: Date?
    /// Set while the session is an overnight loop; absent otherwise.
    public var loop: Loop?

    public init(id: String, short: String? = nil, track: String? = nil, parent: String? = nil, owner: String = "desk", host: String? = "background", name: String? = nil, status: String = "idle", waitingFor: String? = nil, activity: String? = nil, subagents: [Subagent]? = nil, lastAnswer: String? = nil, changes: Changes? = nil, seenAt: Date? = nil, updatedAt: Date? = nil, loop: Loop? = nil) {
        self.id = id
        self.short = short
        self.track = track
        self.parent = parent
        self.owner = owner
        self.host = host
        self.name = name
        self.status = status
        self.waitingFor = waitingFor
        self.activity = activity
        self.subagents = subagents
        self.lastAnswer = lastAnswer
        self.changes = changes
        self.seenAt = seenAt
        self.updatedAt = updatedAt
        self.loop = loop
    }

    /// An unknown status reads as idle, so a newer server never breaks the list.
    public var state: SessionStatus { SessionStatus(rawValue: status) ?? .idle }
    public var isEmils: Bool { owner == "emil" }
    /// The loop label, then the activity: how a live loop's detail line reads.
    public func loopLead(_ activity: String?, timeZone: TimeZone = .current) -> String? {
        guard let loop, loop.isLive else { return activity }
        return [loop.label(timeZone: timeZone), activity].compactMap { $0 }.joined(separator: " · ")
    }
    /// What the agent is doing now, while it works; nothing for a session that is not working.
    public var liveActivity: String? {
        guard state == .working, let a = activity?.trimmingCharacters(in: .whitespaces), !a.isEmpty else { return nil }
        return a
    }
    /// The first line of the session's last answer, plain: what it said when it last stopped.
    public var lastAnswerGist: String? {
        guard let first = lastAnswer?.split(whereSeparator: \.isNewline).first else { return nil }
        let plain = first.replacingOccurrences(of: "`", with: "").replacingOccurrences(of: "**", with: "")
            .trimmingCharacters(in: .whitespaces)
        return plain.isEmpty ? nil : plain
    }
    public var displayName: String { name?.isEmpty == false ? name! : (short ?? String(id.prefix(8))) }
    public var shortID: String { short ?? String(id.prefix(8)) }
    public var isUnseenDone: Bool { state == .done && seenAt == nil }
    public var isLanded: Bool { changes?.merged == true }
    /// Blocked on a prompt a hook answers: a permission, or an MCP approval ("approval in gatekeeper: …").
    public var waitsOnPermission: Bool {
        guard state == .blocked, let w = waitingFor else { return false }
        return w.localizedCaseInsensitiveContains("permission") || w.hasPrefix("approval in ")
    }
}

public struct Changes: Codable, Equatable, Sendable {
    public var added: Int
    public var removed: Int
    public var files: Int?
    public var branch: String?
    public var merged: Bool?

    public init(added: Int, removed: Int, files: Int? = nil, branch: String? = nil, merged: Bool? = nil) {
        self.added = added
        self.removed = removed
        self.files = files
        self.branch = branch
        self.merged = merged
    }
}

public struct Option: Codable, Equatable, Sendable {
    public var label: String
    public var description: String?
    /// A picture, page or file of this option's own.
    public var attachment: Attachment?

    public init(label: String, description: String? = nil, attachment: Attachment? = nil) {
        self.label = label
        self.description = description
        self.attachment = attachment
    }
}

public enum AttachmentKind: String, Sendable {
    case image, page, file
}

/// A picture, page or file a question or one of its options points at (docs/serve-api.md).
public struct Attachment: Codable, Equatable, Sendable {
    public var label: String
    public var url: String
    public var kind: String

    public init(label: String, url: String, kind: String) {
        self.label = label
        self.url = url
        self.kind = kind
    }

    /// An unknown kind reads as a file, so a newer server never breaks the card.
    public var attachmentKind: AttachmentKind { AttachmentKind(rawValue: kind) ?? .file }
    public var target: URL? { URL(string: url) }
    public var isLocal: Bool { target?.isFileURL == true }
    /// The file's path for a `file://` attachment, shown greyed when the file is gone.
    public var localPath: String? { isLocal ? target?.path : nil }
    /// Whether it can be shown now: a file that exists, or a web address.
    public var isAvailable: Bool { localPath.map { FileManager.default.fileExists(atPath: $0) } ?? (target != nil) }
}

public struct Evidence: Codable, Equatable, Sendable {
    public var text: String
    public var source: String?

    public init(text: String, source: String? = nil) {
        self.text = text
        self.source = source
    }
}

/// A recorded answer. The contract doesn't fix its fields, so any non-null value counts as answered.
public struct AnswerRecord: Codable, Equatable, Sendable {
    public var option: Int?
    public var text: String?
    public var by: String?
    public var at: Date?
    public var note: String?

    public init(option: Int? = nil, text: String? = nil, by: String? = nil, at: Date? = nil, note: String? = nil) {
        self.option = option
        self.text = text
        self.by = by
        self.at = at
        self.note = note
    }

    public init(from decoder: any Decoder) throws {
        guard let c = try? decoder.container(keyedBy: CodingKeys.self) else {
            self.init(text: try? decoder.singleValueContainer().decode(String.self))
            return
        }
        self.init(
            option: try? c.decodeIfPresent(Int.self, forKey: .option),
            text: try? c.decodeIfPresent(String.self, forKey: .text),
            by: try? c.decodeIfPresent(String.self, forKey: .by),
            at: try? c.decodeIfPresent(Date.self, forKey: .at),
            note: try? c.decodeIfPresent(String.self, forKey: .note)
        )
    }
}

public struct Question: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var track: String?
    public var session: String?
    public var from: String?
    public var question: String
    public var options: [Option]
    public var recommended: Int?
    public var recommendation: String?
    public var evidence: [Evidence]?
    public var links: [String]?
    public var attachments: [Attachment]?
    public var createdAt: Date?
    public var answer: AnswerRecord?
    /// "queued" while an answer is recorded and waits to be typed into the track's agent, "delivered" once it was.
    public var delivery: String?

    public init(id: String, track: String? = nil, session: String? = nil, from: String? = "desk", question: String, options: [Option] = [], recommended: Int? = nil, recommendation: String? = nil, evidence: [Evidence]? = nil, links: [String]? = nil, attachments: [Attachment]? = nil, createdAt: Date? = nil, answer: AnswerRecord? = nil, delivery: String? = nil) {
        self.id = id
        self.track = track
        self.session = session
        self.from = from
        self.question = question
        self.options = options
        self.recommended = recommended
        self.recommendation = recommendation
        self.evidence = evidence
        self.links = links
        self.attachments = attachments
        self.createdAt = createdAt
        self.answer = answer
        self.delivery = delivery
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        track = try c.decodeIfPresent(String.self, forKey: .track)
        session = try c.decodeIfPresent(String.self, forKey: .session)
        from = try c.decodeIfPresent(String.self, forKey: .from)
        question = try c.decode(String.self, forKey: .question)
        options = try c.decodeIfPresent([Option].self, forKey: .options) ?? []
        recommended = try c.decodeIfPresent(Int.self, forKey: .recommended)
        recommendation = try c.decodeIfPresent(String.self, forKey: .recommendation)
        evidence = try c.decodeIfPresent([Evidence].self, forKey: .evidence)
        links = try c.decodeIfPresent([String].self, forKey: .links)
        attachments = try c.decodeIfPresent([Attachment].self, forKey: .attachments)
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt)
        answer = try c.decodeIfPresent(AnswerRecord.self, forKey: .answer)
        delivery = try c.decodeIfPresent(String.self, forKey: .delivery)
    }

    public var isOpen: Bool { answer == nil }

    /// The question's http(s) links, such as the page an MCP approval asks Emil to open.
    public var openableLinks: [URL] {
        (links ?? []).compactMap { s in
            guard let url = URL(string: s.trimmingCharacters(in: .whitespaces)), url.scheme == "https" || url.scheme == "http", url.host() != nil else { return nil }
            return url
        }
    }

    /// Answered, and the answer is waiting for the agent to stop.
    public var isQueued: Bool { answer != nil && delivery == "queued" }

    /// Every attachment: the question's own, then each option's, in order.
    public var allAttachments: [Attachment] { (attachments ?? []) + options.compactMap(\.attachment) }

    /// The attachments Quick Look pages through: images that exist, the question's own first.
    public var previewableImages: [Attachment] { allAttachments.filter { $0.attachmentKind == .image && $0.isLocal && $0.isAvailable } }

    /// A visual choice: more than one option and every one has a picture, so the composer shows a row of pictures to pick from.
    public var isVisualChoice: Bool { options.count > 1 && options.allSatisfy { $0.attachment?.attachmentKind == .image } }

    /// Who asked: the session that asked, else the track and the kind of asker, else the kind. Answers to one
    /// asker's questions go as one message. Matches askerKey in internal/tracks/answers_batch.go.
    public var asker: String {
        if let session, !session.isEmpty { return "session \(session)" }
        if let track, !track.isEmpty { return "track \(track) from \(from ?? "")" }
        return "from \(from ?? "")"
    }

    /// The dialog the question is answered in: a track's open decisions (its agent's asks and the ones the desk raised
    /// about it) show and are answered together, since they can bear on each other; anything else is its asker's.
    public var dialog: String {
        if let track, !track.isEmpty { return "track \(track)" }
        return asker
    }

    /// Where it sits in its track's dialog: other decisions first, then merge, install and close, in the order they
    /// can happen. Read from the question's words, since askers don't tag their asks.
    public var dialogOrder: Int {
        let text = question.lowercased()
        if text.contains("close this track") || text.hasPrefix("close ") { return 3 }
        if text.contains("install") { return 2 }
        if text.contains("merge") { return 1 }
        return 0
    }

    /// An answer in words: the option's label, or the text given.
    public func label(for choice: AnswerChoice) -> String {
        switch choice {
        case .option(let n): options.indices.contains(n) ? options[n].label : "option \(n + 1)"
        case .text(let text): text
        }
    }

    /// Options and no picture anywhere: Emil can ask the agent to render each option to an image.
    public var canShowOptions: Bool { !options.isEmpty && allAttachments.isEmpty }

    /// The recommended option's index, when it names an option that exists.
    public var recommendedIndex: Int? {
        guard let r = recommended, options.indices.contains(r) else { return nil }
        return r
    }

    /// The recommendation in words, for a question that has no options to point at.
    public var recommendationText: String? {
        guard options.isEmpty, let r = recommendation?.trimmingCharacters(in: .whitespacesAndNewlines), !r.isEmpty else { return nil }
        return r
    }

    /// What enter answers: the recommended option, else the recommendation in words.
    public var recommendedChoice: AnswerChoice? {
        if let i = recommendedIndex { return .option(i) }
        if let r = recommendationText { return .text("Go with your recommendation: \(r)") }
        return nil
    }
}

public struct ConversationEntry: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var at: Date?
    public var role: String
    public var text: String
    public var steps: Int?
    public var session: String?
    public var outcome: String?
    /// The track a report came from, kept so a report from a closed track still names it.
    public var track: String?

    public init(id: String, at: Date? = nil, role: String, text: String, steps: Int? = nil, session: String? = nil, outcome: String? = nil, track: String? = nil) {
        self.id = id
        self.at = at
        self.role = role
        self.text = text
        self.steps = steps
        self.session = session
        self.outcome = outcome
        self.track = track
    }
}

public struct Handled: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var at: Date?
    public var track: String?
    public var kind: String?
    public var did: String?
    /// True once Emil took the entry back; the server leaves it out of older snapshots.
    public var undone: Bool?

    public init(id: String, at: Date? = nil, track: String? = nil, kind: String? = nil, did: String? = nil, undone: Bool? = nil) {
        self.id = id
        self.at = at
        self.track = track
        self.kind = kind
        self.did = did
        self.undone = undone
    }

    /// What the server can take back: the desk's answers, starts and closes (u in the pane's handled log).
    public var canUndo: Bool { undone != true && (kind == "closed" || kind == "answered" || kind == "started") }

    /// The button's label: a close is reopened, the others taken back.
    public var undoLabel: String { kind == "closed" ? "Reopen" : "Take back" }
}

/// `GET /v1/sessions/{id}/transcript`.
public struct Transcript: Codable, Equatable, Sendable {
    public var entries: [TranscriptEntry]

    public init(entries: [TranscriptEntry]) { self.entries = entries }
}

public struct TranscriptEntry: Codable, Equatable, Sendable {
    public var role: String
    public var text: String
    public var steps: Int?
    public var at: Date?

    public init(role: String, text: String, steps: Int? = nil, at: Date? = nil) {
        self.role = role
        self.text = text
        self.steps = steps
        self.at = at
    }
}

import DeskCore
import SwiftUI

/// The desk's chat: Emil's messages, the desk's answers with their tool steps folded, and reports as cards.
struct ChatView: View {
    var store: DeskStore
    var model: WindowModel

    var body: some View {
        let s = store.snapshot
        let board = store.board
        ArrivalScroll(ids: s.conversation.map(\.id), scrollRequest: model.scrollRequest, windowVisible: model.windowVisible) {
            LazyVStack(alignment: .leading, spacing: Space.none) {
                let kinds = s.conversation.map { Self.kind(of: $0) }
                ForEach(Array(s.conversation.enumerated()), id: \.element.id) { i, entry in
                    let prev = i > 0 ? (kind: kinds[i - 1], at: s.conversation[i - 1].at) : nil
                    Group {
                        switch entry.role {
                        case "emil":
                            EmilBubble(text: entry.text)
                        case "report":
                            let session = s.session(entry.session)
                            ReportCard(entry: entry, session: session, readyToLand: session.map { board.readyToLand.contains($0) } ?? false)
                        default:
                            if entry.outcome == "reasked" {
                                ReaskedMessage(text: entry.text, at: entry.at)
                            } else {
                                let named = DecisionRefs.questions(in: entry.text, from: s)
                                VStack(alignment: .leading, spacing: Space.s) {
                                    // Open questions are links in the text that load the composer; only answered ones get a line.
                                    AgentMessage(who: "Prism", text: entry.text, steps: entry.steps, at: entry.at, showsName: !Rhythm.joins(kinds[i], at: entry.at, after: prev), openIDs: Set(named.filter(\.isOpen).map(\.id))) { model.pick($0) }
                                    ForEach(named.filter { !$0.isOpen }) { q in
                                        DecisionRecord(question: q)
                                    }
                                }
                            }
                        }
                    }
                    .padding(.top, Rhythm.gap(kinds[i], at: entry.at, after: prev))
                    .arrives(entry.id)
                }
            }
            .padding(.horizontal, Space.m)
            .padding(.vertical, Space.xl)
            .frame(maxWidth: Theme.chatMaxWidth)
            .frame(maxWidth: .infinity)
        }
        .scrollsBelowTitle()
    }

    private static func kind(of entry: ConversationEntry) -> Rhythm.Kind {
        switch entry.role {
        case "emil": .emil
        case "report": .other
        default: entry.outcome == "reasked" ? .other : .prism
        }
    }
}

struct EmilBubble: View {
    var text: String

    var body: some View {
        HStack {
            Spacer(minLength: Space.xxl * 3)
            Text(Theme.markdown(text))
                .foregroundStyle(Theme.onInk)
                .textSelection(.enabled)
                .padding(.horizontal, Space.l)
                .padding(.vertical, Space.s)
                .background(Theme.ink, in: RoundedRectangle(cornerRadius: Theme.bubbleRadius, style: .continuous))
        }
    }
}

/// A message from the desk or an assistant turn in a transcript, with its tool calls folded to a count.
struct AgentMessage: View {
    var who: String
    var text: String
    var steps: Int?
    var at: Date?
    /// A follow-up within two minutes of Prism's last message drops its name line.
    var showsName = true
    /// D ids in `text` that are still open: drawn as magenta links that call `pick`.
    var openIDs: Set<String> = []
    var pick: (String) -> Void = { _ in }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            if showsName {
                HStack(spacing: Space.s) {
                    Text(who).font(.callout.weight(.semibold))
                    if let at { Text(at.formatted(Theme.time)).font(.prismMeta).foregroundStyle(.secondary) }
                }
            }
            Text(Self.linked(text, open: openIDs))
                .textSelection(.enabled)
                .prose()
                .fixedSize(horizontal: false, vertical: true)
                .environment(\.openURL, OpenURLAction { url in
                    guard url.scheme == "prism-decision", let id = url.host() ?? url.host else { return .systemAction }
                    pick(id.uppercased())
                    return .handled
                })
            if let steps, steps > 0 {
                Text("\(steps) step\(steps == 1 ? "" : "s")")
                    .font(.prismMeta)
                    .foregroundStyle(.secondary)
                    .help("Tool calls, folded")
            }
        }
        .conversationText()
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

extension AgentMessage {
    /// The message's Markdown with each open D id turned into a magenta link.
    static func linked(_ text: String, open: Set<String>) -> AttributedString {
        var out = Theme.markdown(text)
        for id in open {
            var from = out.startIndex
            while let r = out[from...].range(of: id) {
                let before = r.lowerBound > out.startIndex ? out.characters[out.characters.index(before: r.lowerBound)] : nil
                let after = r.upperBound < out.endIndex ? out.characters[r.upperBound] : nil
                let wordy: (Character?) -> Bool = { $0.map { $0.isLetter || $0.isNumber } ?? false }
                if !wordy(before), !wordy(after), let url = URL(string: "prism-decision://\(id)") {
                    out[r].link = url
                    out[r].foregroundColor = Theme.question
                    out[r].font = .prismID
                }
                from = r.upperBound
            }
        }
        return out
    }
}

/// An answered decision, one line: its id, what was asked, the answer.
struct DecisionRecord: View {
    var question: Question

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(question.id).font(.prismID)
            Text(Theme.markdown(question.question)).lineLimit(1)
            Text(question.chipSummary.replacingOccurrences(of: "answered: ", with: "")).fontWeight(.semibold).lineLimit(1)
            Image(systemName: "checkmark")
        }
        .font(.prismMeta)
        .foregroundStyle(.secondary)
        .conversationText()
    }
}

/// A desk reply prism server hid because it numbered questions in chat; the desk raised them again as decisions.
struct ReaskedMessage: View {
    var text: String
    var at: Date?

    var body: some View {
        DisclosureGroup {
            Text(Theme.markdown(text))
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        } label: {
            HStack(spacing: Space.s) {
                Text("Prism re-asked these as decisions").font(.callout).foregroundStyle(.secondary)
                if let at { Text(at.formatted(Theme.time)).font(.prismMeta).foregroundStyle(.secondary) }
            }
        }
        .conversationText()
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A decision the desk named by its D id: the question, its option keys, open or answered. Clicking an open
/// one brings it to the composer, where enter and 1–9 answer it.
struct DecisionChip: View {
    var question: Question
    var picked: Bool
    var pick: () -> Void

    var body: some View {
        Button(action: pick) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Text(question.id)
                    .font(.prismID)
                    .foregroundStyle(question.isOpen ? AnyShapeStyle(Theme.question) : AnyShapeStyle(.secondary))
                VStack(alignment: .leading, spacing: Space.xxs) {
                    Text(Theme.questionMarkdown(question.question)).font(.callout.weight(.medium)).lineLimit(2)
                    Text(question.chipSummary).font(.prismMeta).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: Space.s)
                if question.isOpen {
                    Pill(text: picked ? "In the composer" : "Answer", color: Theme.question)
                } else {
                    Image(systemName: "checkmark").foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, Space.m)
            .padding(.vertical, Space.s)
            .overlay {
                if picked { RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).strokeBorder(.tint) }
            }
        }
        .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius, card: true))
        .disabled(!question.isOpen)
        .help(question.isOpen ? "Answer \(question.id) in the composer" : "\(question.id) is answered")
    }
}

/// A session's report to the desk, as a card in the chat.
struct ReportCard: View {
    var entry: ConversationEntry
    var session: Session?
    var readyToLand: Bool
    @State private var expanded = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    /// The session's name, else the track stored on the report with "closed track", else "Closed session".
    private var name: String {
        if let n = session?.displayName { return n }
        if let t = entry.track, !t.isEmpty { return "\(t) (closed track)" }
        return "Closed session"
    }

    private var isEmpty: Bool { entry.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    /// List markers become their own lines, so a clamp never cuts between a dash and its text.
    private var lines: [String] {
        entry.text.split(separator: "\n", omittingEmptySubsequences: true).map { raw in
            let line = raw.trimmingCharacters(in: .whitespaces)
            for marker in ["- ", "* ", "\u{2022} "] where line.hasPrefix(marker) { return "\u{2022} " + line.dropFirst(marker.count) }
            return line
        }
    }

    private var isLong: Bool { lines.count > 3 || entry.text.count > 280 }

    var body: some View {
        if isEmpty {
            HStack(spacing: Space.s) {
                Dot(color: .secondary)
                Text(name).font(.callout.weight(.semibold))
                Text("stopped without a report").font(.callout).foregroundStyle(.secondary)
                Spacer(minLength: Space.s)
                if let at = entry.at { Text(at.formatted(Theme.time)).font(.prismMeta).foregroundStyle(.secondary) }
            }
            .conversationText()
        } else {
            card
        }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                Dot(color: color)
                Text(name).font(.callout.weight(.semibold))
                Text(outcome).font(.callout).foregroundStyle(.secondary)
                Spacer(minLength: Space.s)
                if let at = entry.at { Text(at.formatted(Theme.time)).font(.prismMeta).foregroundStyle(.secondary) }
            }
            Text(Theme.markdown(lines.joined(separator: "\n")))
                .lineLimit(expanded ? nil : 3)
                .textSelection(.enabled)
                .prose()
                .fixedSize(horizontal: false, vertical: true)
            if isLong {
                Button(expanded ? "Show less" : "Show more") { withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { expanded.toggle() } }
                    .buttonStyle(.plain)
                    .font(.callout)
                    .foregroundStyle(.tint)
            }
            if let changes = session?.changes {
                HStack(spacing: Space.s) {
                    DiffStat(changes: changes)
                    if let files = changes.files { Text("\(files) file\(files == 1 ? "" : "s")").font(.prismMeta).foregroundStyle(.secondary) }
                    if let branch = changes.branch { Text(branch).font(.prismMono).foregroundStyle(.secondary).lineLimit(1) }
                    Spacer(minLength: Space.s)
                    if readyToLand { Pill(text: "Ready to land", color: Theme.done) }
                }
            }
        }
        .padding(Theme.cardPadding)
        .background(Theme.cardFill, in: RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).strokeBorder(Theme.hairline))
    }

    private var color: Color {
        switch entry.outcome {
        case "done": Theme.done
        case "blocked", "error": Theme.alert
        default: .secondary
        }
    }

    private var outcome: String {
        switch entry.outcome {
        case "done": return "finished"
        case "error": return "failed"
        case "blocked":
            if let w = session?.waitingFor, !w.isEmpty { return "waiting on a \(w)" }
            return "blocked"
        case let other?: return other
        case nil: return ""
        }
    }
}

/// A background session's transcript, read-only, with Take Over (or Give Back when it is Emil's).
struct TranscriptView: View {
    var store: DeskStore
    var model: WindowModel
    var session: Session
    @State private var transcript: Transcript?
    @State private var failure: String?

    private static func kind(of entry: TranscriptEntry) -> Rhythm.Kind {
        switch entry.role {
        case "user": .emil
        case "report": .other
        default: .prism
        }
    }

    var body: some View {
        VStack(spacing: Space.none) {
            ArrivalScroll(ids: (transcript?.entries ?? []).indices.map(String.init), scrollRequest: nil, windowVisible: model.windowVisible) {
                LazyVStack(alignment: .leading, spacing: Space.none) {
                    if let failure {
                        Text(failure).foregroundStyle(.secondary).conversationText()
                    } else if let transcript, transcript.entries.isEmpty {
                        Text("No transcript yet.").foregroundStyle(.secondary).conversationText()
                    }
                    let entries = transcript?.entries ?? []
                    let kinds = entries.map { Self.kind(of: $0) }
                    ForEach(Array(entries.enumerated()), id: \.offset) { i, entry in
                        let prev = i > 0 ? (kind: kinds[i - 1], at: entries[i - 1].at) : nil
                        Group {
                            switch entry.role {
                            case "user": EmilBubble(text: entry.text)
                            case "report": ReportCard(entry: ConversationEntry(id: "", at: entry.at, role: "report", text: entry.text, session: session.id, outcome: session.state.rawValue), session: session, readyToLand: false)
                            default: AgentMessage(who: session.displayName, text: entry.text, steps: entry.steps, at: entry.at, showsName: !Rhythm.joins(kinds[i], at: entry.at, after: prev))
                            }
                        }
                        .padding(.top, Rhythm.gap(kinds[i], at: entry.at, after: prev))
                        .arrives(String(i))
                    }
                    if let activity = session.activity, !activity.isEmpty, session.state == .working || session.state == .blocked {
                        Label(activity, systemImage: session.state == .working ? "ellipsis" : "hand.raised")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .conversationText()
                            .padding(.top, Space.m)
                    }
                }
                .padding(.horizontal, Space.m)
                .padding(.vertical, Space.xl)
                .frame(maxWidth: Theme.chatMaxWidth)
                .frame(maxWidth: .infinity)
            }
            .scrollsBelowTitle()
            OwnerBar(store: store, model: model, session: session)
        }
        .task(id: "\(session.id) \(session.updatedAt?.timeIntervalSince1970 ?? 0)") {
            do {
                transcript = try await store.transcript(session.id)
                failure = nil
            } catch {
                failure = "Couldn't load the transcript. \(error.localizedDescription)"
            }
        }
    }
}

/// Under a transcript: whose session it is, and the one action that changes that.
struct OwnerBar: View {
    var store: DeskStore
    var model: WindowModel
    var session: Session
    @FocusState private var focus: ComposerField?

    var body: some View {
        VStack(spacing: Space.s) {
            if let q = model.activeQuestion(in: store.snapshot), q.session == session.id {
                QuestionComposer(store: store, model: model, question: q, position: nil, focus: $focus)
                    .onChange(of: model.focus) { _, request in
                        if let request, request.field != .desk { focus = request.field }
                    }
            }
            HStack(spacing: Space.m) {
                Image(systemName: session.isEmils ? "person" : "apple.terminal").foregroundStyle(.secondary)
                Text(session.isEmils ? "Yours. Prism leaves it alone." : "Prism's session. Read-only here.")
                    .foregroundStyle(.secondary)
                Spacer()
                if session.isEmils {
                    Button { Task { await store.giveBack(session.id) } } label: {
                        HStack(spacing: Space.s) { Text("Give Back to Prism"); KeyHint(text: "g") }
                    }
                } else {
                    Button { Task { await store.takeOver(session.id) } } label: {
                        HStack(spacing: Space.s) { Text("Take Over"); KeyHint(text: "t") }
                    }
                    .buttonStyle(.borderedProminent)
                }
            }
            .font(.callout)
            .conversationText()
        }
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.m)
        .frame(maxWidth: Theme.chatMaxWidth)
        .frame(maxWidth: .infinity)
    }
}

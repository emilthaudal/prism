import DeskCore
import SwiftUI

/// The board: Needs you, Reports, Tracks with their milestones, Loops, Ready to land, and Landed folded.
struct BoardView: View {
    var store: DeskStore
    var model: WindowModel
    var board: Board
    @State private var landedOpen = false
    @State private var handledOpen = true
    @State private var handledAll = false
    /// Emil's own fold of Handled, kept until the next quiet spell.
    @State private var handledOverride: Bool?
    /// Where each card sat before the latest change, to tell a card leaving or changing section from one arriving.
    @State private var before: [String: String] = [:]
    @State private var primed = false
    /// A demo script brings the Handled heading into view to hover it (EMI-482).
    @State private var scroll = ScrollPosition()
    @State private var recent: Set<String> = []
    @Environment(\.accessibilityReduceMotion) private var reduce
    static let quietShown = 3
    /// Handled opens on its newest few; the rest wait behind "Show all".
    static let handledShown = 6

    /// Entries handled on today's calendar day; the full log waits behind "Show all".
    static func handledToday(_ entries: [Handled], now: Date) -> Int {
        BoardPlan.handledToday(entries, now: now).count
    }

    /// The track's name, or "Prism" for what the desk noted for itself.
    static func handledName(_ entry: Handled, in s: Snapshot) -> String {
        if entry.track == "desk" { return "Prism" }
        return s.track(entry.track)?.name ?? entry.track ?? ""
    }

    var body: some View {
        let s = store.snapshot
        let now = store.now
        let active = model.activeQuestion(in: s)?.id
        let placements = board.placements
        let motion = motion(from: before, to: placements)
        let loops = LoopBrief.all(s)
        ScrollView {
            VStack(alignment: .leading, spacing: Space.m) {
                // Just the name: the window's one status line says the rest.
                Text("Board").font(.prismTitle)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .overlay(alignment: .trailing) { if loops.isEmpty { OvernightButton(model: model) } }
                    .boardText()
                if let digest = Away.digest(lastLooked: model.away.since, now: now, snapshot: s) {
                    AwaySection(digest: digest, model: model, now: now).arrives("away")
                }
                // The board's first answer is always whether anything needs Emil.
                if board.needsYou.isEmpty {
                    HStack(spacing: Space.s) {
                        Dot(color: Theme.done)
                        Text("Nothing needs you").font(.callout).foregroundStyle(.secondary)
                        Spacer(minLength: 0)
                    }
                    .boardText()
                    QuietBoard(plan: BoardPlan(s, now: now))
                        .arrives("quiet")
                }
                if !board.needsYou.isEmpty {
                    SectionHeading("Needs you", count: board.needsYou.count)
                    ForEach(Array(Set(board.needsYou.map(\.dialog))).sorted(), id: \.self) { dialog in
                        StagedBar(store: store, dialog: dialog).padding(.leading, Space.m)
                    }
                    ForEach(board.needsYou) { q in
                        QuestionCard(store: store, question: q, track: s.track(q.track), session: s.session(q.session), selected: q.id == active, now: now) { model.pick(q.id) }
                            .arrives(q.id)
                    }
                }
                if !board.queued.isEmpty {
                    SectionHeading("Answered, waiting to send", count: board.queued.count)
                    ForEach(board.queued) { q in
                        QueuedRow(question: q, track: s.track(q.track), now: now)
                            .arrives(q.id)
                    }
                }
                if !board.reports.isEmpty {
                    SectionHeading("Reports", count: board.reports.count)
                    BoardGroup {
                        ForEach(board.reports) { session in
                            DoneReport(store: store, session: session) { model.selection = .session(session.id) }
                                .arrives(session.id)
                        }
                    }
                }
                // A card per open track with its goal and milestones, then the loops. What a session does now is the
                // sidebar's to say, so the board has no In flight list (EMI-481).
                MilestoneSection(store: store, model: model, now: now)
                LoopsSection(store: store, model: model, loops: loops, now: now)
                if !board.readyToLand.isEmpty {
                    SectionHeading("Ready to land", count: board.readyToLand.count)
                    ForEach(board.readyToLand) { session in
                        LaneRow(session: session, detail: [session.changes?.branch, "not merged"].compactMap { $0 }.joined(separator: " · ")) { model.selection = .session(session.id) }
                        .arrives(session.id)
                    }
                }
                if !board.landed.isEmpty {
                    DisclosureGroup(isExpanded: Binding(get: { landedOpen }, set: { open in withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { landedOpen = open } })) {
                        VStack(spacing: Space.xs) {
                            ForEach(board.landed) { session in
                                LaneRow(session: session, detail: session.changes?.branch ?? "merged")
                            }
                        }
                        .padding(.top, Space.xs)
                    } label: {
                        SectionHeading("Landed", count: board.landed.count)
                    }
                }
                if !s.handled.isEmpty {
                    // A heading that folds, with the rows on the same edge as the other sections (a DisclosureGroup
                    // indents them).
                    Button(action: toggleHandled) {
                        HStack(alignment: .firstTextBaseline, spacing: Space.xs) {
                            Image(systemName: "chevron.right")
                                .font(.prismMeta.weight(.semibold))
                                .foregroundStyle(.secondary)
                                .rotationEffect(.degrees(isOpen ? 90 : 0))
                            SectionHeading("Handled today", count: Self.handledToday(s.handled, now: now), bare: true)
                        }
                        // The shade is the label's own box: even padding inside it, and the chevron on the text edge.
                        .padding(.horizontal, Space.s)
                        .padding(.vertical, Space.xs)
                    }
                    .buttonStyle(RowButtonStyle())
                    .targetID("handled")
                    .id("handled")
                    .padding(.horizontal, Space.m - Space.s)
                    .padding(.top, Space.m - Space.xs)
                    if isOpen {
                        let newest = Array(s.handled.reversed())
                        let shown = handledAll ? newest : Array(newest.prefix(quiet ? Self.quietShown : Self.handledShown))
                        VStack(alignment: .leading, spacing: Space.none) {
                            ForEach(shown) { entry in
                                HandledRow(store: store, entry: entry, trackName: Self.handledName(entry, in: s), now: now)
                            }
                            if newest.count > (quiet ? Self.quietShown : Self.handledShown) {
                                Button(handledAll ? "Show fewer" : "Show all \(newest.count)") {
                                    withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { handledAll.toggle() }
                                }
                                .buttonStyle(.plain)
                                .font(.callout.weight(.medium))
                                .foregroundStyle(.secondary)
                                .padding(.horizontal, Space.m)
                                .padding(.top, Space.xs)
                            }
                        }
                        .padding(.horizontal, -Space.m)
                    }
                }
            }
            .scrollTargetLayout()
            .padding(Space.l)
            .environment(\.arrivals, motion.arrivals)
            .animation(motion.layout, value: placements)
        }
        .scrollPosition($scroll)
        .onChange(of: model.hoveredTarget) { _, target in if target == "handled" { scroll.scrollTo(id: "handled", anchor: .center) } }
        .onAppear {
            before = placements
            primed = true
        }
        .onChange(of: placements) { old, new in
            recent = Set(new.filter { old[$0.key] != $0.value }.keys)
            before = new
        }
        .onChange(of: quiet) { _, now in if !now { handledOverride = nil } }
        .onChange(of: model.foldRequest) { _, request in if request?.target == "handled" { toggleHandled() } }
    }

    /// How the board changes: a session changing section settles, a card leaving closes the gap as it fades, a card
    /// arriving makes room. Nothing animates on first load or while the window is not on screen.
    private func motion(from old: [String: String], to new: [String: String]) -> (arrivals: Arrivals, layout: Animation?) {
        guard model.windowVisible, primed else { return (Arrivals(), nil) }
        let moved = new.contains { id, section in old[id].map { $0 != section } ?? false }
        let left = old.keys.contains { new[$0] == nil }
        let layout = moved ? Motion.settle : (left ? Motion.quick : Motion.arrive)
        let fresh = Set(new.filter { old[$0.key] != $0.value }.keys).union(recent)
        return (Arrivals(fade: moved ? Motion.settle : Motion.fade, fresh: fresh, leaving: reduce ? nil : Motion.fade), Motion.moves(layout, reduce: reduce))
    }

    /// Nothing needs Emil and nothing runs: the board says what just happened.
    private var quiet: Bool { board.needsYou.isEmpty && board.inFlight.isEmpty }
    /// Handled is open by its own fold, or by the quiet state unless Emil folded it in this spell.
    private var isOpen: Bool { handledOverride ?? (handledOpen || quiet) }

    private func toggleHandled() {
        withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { handledOverride = !isOpen }
    }
}

/// A board section's rows as one soft sheet, with a hairline between them: five tracks read as one object, and a
/// milestone card that folds out grows inside it (EMI-480).
struct BoardGroup<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
        VStack(alignment: .leading, spacing: Space.none) {
            Group(subviews: content) { rows in
                ForEach(Array(rows.enumerated()), id: \.element.id) { i, row in
                    if i > 0 { Rectangle().fill(Theme.hairline).frame(height: 1).padding(.horizontal, Theme.boardCardPadding) }
                    row
                }
            }
        }
        .background(Theme.cardFill, in: shape)
        .clipShape(shape)
    }
}

struct SectionHeading: View {
    var title: String
    var count: String

    /// Inside a button that pads its own label: no padding here, so a hover shade fits the text.
    var bare = false

    init(_ title: String, count: Int, bare: Bool = false) {
        self.title = title
        self.count = "\(count)"
        self.bare = bare
    }

    init(_ title: String, detail: String) {
        self.title = title
        self.count = detail
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(title).font(.callout.weight(.semibold)).foregroundStyle(.secondary)
            Text(count).font(.callout.monospacedDigit()).foregroundStyle(.secondary)
        }
        .padding(.top, bare ? 0 : Space.m)
        .padding(.horizontal, bare ? 0 : Space.m)
    }
}

/// One thing the desk did on its own. A close, answer or start Emil can take back gets a button that does what u does in the pane.
struct HandledRow: View {
    var store: DeskStore
    var entry: Handled
    var trackName: String
    var now: Date

    @State private var hovering = false
    @Environment(WindowModel.self) private var model: WindowModel?

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xxs) {
            HStack(alignment: .firstTextBaseline, spacing: Space.xs) {
                Text(trackName)
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let kind = entry.kind, !kind.isEmpty {
                    Text(kind).font(.subheadline).foregroundStyle(.secondary).fixedSize()
                }
                Spacer(minLength: Space.s)
                // The way back is there when the pointer is on the row; at rest the list reads as a log.
                if entry.undone == true {
                    Text("Taken back").font(.prismMeta).foregroundStyle(.secondary).fixedSize()
                } else if entry.canUndo && hovering {
                    Button(entry.undoLabel) { Task { await store.undoHandled(entry.id) } }
                        .buttonStyle(.link)
                        .font(.subheadline.weight(.medium))
                        .fixedSize()
                } else {
                    Text(Theme.ago(entry.at, now: now)).font(.prismMeta).foregroundStyle(.secondary).fixedSize()
                }
            }
            if let did = entry.did, !did.isEmpty {
                Text(did)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, Space.s)
        .padding(.horizontal, Space.m)
        .contentShape(Rectangle())
        .onHover { hovering = $0 && (model?.pointerLive ?? true) }
        .contextMenu {
            if entry.canUndo { Button(entry.undoLabel) { Task { await store.undoHandled(entry.id) } } }
        }
        .opacity(entry.undone == true ? 0.6 : 1)
    }
}

/// An answer recorded while its agent was busy: one quiet line, until the agent stops and takes it.
struct QueuedRow: View {
    var question: Question
    var track: Track?
    var now: Date

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Image(systemName: "clock").font(.prismMeta).foregroundStyle(.secondary).frame(width: Space.l)
            VStack(alignment: .leading, spacing: Space.xxs) {
                Text("\(question.id) \(track?.name ?? "Prism")").font(.callout.weight(.medium)).lineLimit(1)
                Text("Sent when its agent stops").font(.prismMeta).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: Space.s)
        }
        .padding(.vertical, Space.xs)
        .padding(.horizontal, Space.m)
    }
}

/// One decision: the id, where it came from, the evidence, and an answer in one click.
struct QuestionCard: View {
    var store: DeskStore
    var question: Question
    var track: Track?
    var session: Session?
    var selected: Bool
    @Environment(\.accessibilityReduceMotion) private var reduce
    @Environment(WindowModel.self) private var model: WindowModel?
    var now: Date
    var pick: () -> Void

    var body: some View {
        Button(action: pick) { card }
            .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius, card: true, decides: true))
            .targetID("card:\(question.id)")
    }

    @ViewBuilder
    private var card: some View {
        let permission = session?.waitsOnPermission == true
        let counting = store.answers.pending(for: question.id) != nil || store.answers.batch(containing: question.id) != nil
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                Image(systemName: "diamond.fill")
                    .font(.prismMeta)
                    .foregroundStyle(permission ? Theme.alert : Theme.question)
                Text(question.id).font(.prismID).foregroundStyle(.secondary).fixedSize()
                if let lane = Theme.lane(of: track?.id) { LaneChip(color: lane) }
                Text(track?.name ?? session?.displayName ?? "Prism").font(.callout.weight(.semibold)).lineLimit(1).layoutPriority(1)
                Spacer(minLength: Space.xs)
                Text([question.from.map { $0 == "agent" ? "agent" : "from \($0)" }, Theme.ago(question.createdAt, now: now)].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.prismMeta)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .fixedSize()
            }
            // The card is a summary: a long ask reads in full in the composer.
            Text(Theme.questionMarkdown(question.question))
                .font(.prismQuestion)
                .lineLimit(6)
                .fixedSize(horizontal: false, vertical: true)
            if let evidence = question.evidence, !evidence.isEmpty {
                VStack(alignment: .leading, spacing: Space.xs) {
                    ForEach(Array(evidence.enumerated()), id: \.offset) { _, line in
                        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                            Image(systemName: "checkmark").font(.prismMeta.weight(.bold)).foregroundStyle(Theme.done)
                            Text(line.text)
                            if let source = line.source { Text(source).italic().foregroundStyle(.secondary) }
                        }
                        .font(.prismMeta)
                    }
                }
            }
            AttachmentStrip(question: question, thumbnailHeight: Theme.cardThumbnail)
            if question.options.contains(where: { $0.attachment != nil }) {
                OptionAttachments(question: question)
            }
            if PourCardActions.fits(question, in: store) {
                PourCardActions(store: store, question: question)
            } else if let pending = store.answers.pending(for: question.id) {
                CountdownRow(store: store, pending: pending, named: false).transition(Motion.fadeTransition)
            } else if let batch = store.answers.batch(containing: question.id) {
                BatchCountdownRow(store: store, batch: batch).transition(Motion.fadeTransition)
            } else if let choice = store.stagedChoice(for: question.id) {
                StagedRow(label: question.label(for: choice)) { store.unstage(question.id) }
            } else if selected {
                // The composer has this one; answering twice from two places is what this line prevents.
                Label("Answering in the composer", systemImage: "circle.fill")
                    .labelStyle(AnsweringLabel())
                    .font(.prismMeta)
                    .foregroundStyle(.secondary)
                    .transition(Motion.fadeTransition)
            } else {
                Group {
                    if let text = question.recommendationText {
                        Text(Theme.markdown("Recommended: \(text)"))
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    OptionButtons(question: question) { i in store.answer(question.id, choice: .option(i)) }
                }
                .transition(Motion.fadeTransition)
            }
        }
        .animation(Motion.moves(Motion.answer, reduce: reduce), value: counting)
        .animation(Motion.moves(Motion.settle, reduce: reduce), value: selected)
        .padding(Theme.boardCardPadding)
        .overlay {
            RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                .strokeBorder(.tint, lineWidth: 1.5)
                .opacity(selected ? 1 : 0)
                .animation(Motion.fades(Motion.settle, reduce: reduce), value: selected)
                // Only the opacity animates: the ring's size follows the card, which jumps under Reduce Motion.
                .geometryGroup()
        }
    }
}

/// A small accent dot, then the text: where the answer is being written.
struct AnsweringLabel: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: Space.xs) {
            Circle().fill(.tint).frame(width: Theme.dotSize, height: Theme.dotSize)
            configuration.title
        }
    }
}

/// What each option points at: its picture with the option's number, or an Open button for a page or file.
struct OptionAttachments: View {
    var question: Question

    var body: some View {
        HStack(alignment: .top, spacing: Space.s) {
            ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                if let a = option.attachment {
                    VStack(spacing: Space.xxs) {
                        if a.attachmentKind == .image {
                            AttachmentThumbnail(attachment: a, height: Theme.cardThumbnail) { Attachments.open(a, among: question) }
                        } else {
                            AttachmentButton(attachment: a) { Attachments.open(a, among: question) }
                        }
                        Text("\(i + 1) \(option.label)").font(.prismMeta).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
            }
        }
    }
}

/// The options as buttons, the recommended one prominent; they wrap to a column when they don't fit a row.
struct OptionButtons: View {
    var question: Question
    var pick: (Int) -> Void

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: Space.s) { buttons }
            VStack(alignment: .leading, spacing: Space.xs) { buttons }
        }
        .controlSize(.small)
    }

    @ViewBuilder
    private var buttons: some View {
        ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
            if i == question.recommendedIndex {
                Button { pick(i) } label: { Text(option.label).lineLimit(1) }
                    .buttonStyle(.borderedProminent)
                    .help(option.description ?? "")
            } else {
                Button { pick(i) } label: { Text(option.label).lineLimit(1) }
                    .buttonStyle(.bordered)
                    .help(option.description ?? "")
            }
        }
    }
}

/// A session on the board: what it does now and how much it changed.
struct LaneRow: View {
    var session: Session
    var detail: String
    /// Opens the session; a row with nothing to open is plain.
    var open: (() -> Void)?

    var body: some View {
        if let open {
            Button(action: open) { row }.buttonStyle(RowButtonStyle())
        } else {
            row
        }
    }

    private var row: some View {
        HStack(spacing: Space.s) {
            ChipIcon(chip: Sidebar.chip(for: session, questions: []), lane: Theme.lane(of: session.track))
                .frame(width: Space.l)
            VStack(alignment: .leading, spacing: Space.xxs) {
                Text(session.displayName).font(.body.weight(.medium)).lineLimit(1).truncationMode(.middle)
                Text(detail).font(.subheadline).foregroundStyle(.secondary).lineLimit(1).truncationMode(.tail)
            }
            Spacer(minLength: Space.s)
            if let changes = session.changes { DiffStat(changes: changes) }
        }
        .padding(.vertical, Space.xs)
        .padding(.horizontal, Space.m)
        .contentShape(Rectangle())
        .opacity(session.state == .idle ? 0.6 : 1)
        .help("\(session.displayName)\n\(detail)")
    }
}

/// A finished session's report: its outcome, its last answer and the git facts, until Emil dismisses or opens it.
struct DoneReport: View {
    var store: DeskStore
    var session: Session
    var open: () -> Void

    var body: some View {
        Button(action: open) { card }
            .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius, card: true, informs: true))
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                Dot(color: Theme.done)
                Text(session.displayName).font(.callout.weight(.semibold)).lineLimit(1)
                Text("finished").font(.callout).foregroundStyle(.secondary)
                Spacer(minLength: Space.s)
                Button("Dismiss") { Task { await store.opened(session.id) } }
                    .buttonStyle(.link)
                    .font(.prismMeta)
            }
            if let answer = session.lastAnswer?.trimmingCharacters(in: .whitespacesAndNewlines), !answer.isEmpty {
                Text(Theme.markdown(answer)).font(.callout).lineLimit(6).fixedSize(horizontal: false, vertical: true)
            }
            if let changes = session.changes {
                HStack(spacing: Space.s) {
                    DiffStat(changes: changes)
                    if let files = changes.files { Text("\(files) file\(files == 1 ? "" : "s")").font(.prismMeta).foregroundStyle(.secondary) }
                    if let branch = changes.branch { Text(branch).font(.prismMono).foregroundStyle(.secondary).lineLimit(1) }
                    Spacer(minLength: Space.s)
                    Pill(text: changes.merged == true ? "Merged" : "Not merged", color: Theme.done)
                }
            }
        }
        .padding(Theme.boardCardPadding)
    }
}

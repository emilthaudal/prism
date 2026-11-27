import DeskCore
import SwiftUI

/// The sessions: the desk first, then tracks by mode with the desk's sessions nested under their track.
struct SidebarView: View {
    var store: DeskStore
    var model: WindowModel
    @State private var parkedOpen = false
    @State private var folds = SidebarFolds()
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        @Bindable var model = model
        let s = store.snapshot
        let sidebar = Sidebar(s)
        let now = store.now
        List(selection: $model.selection) {
            DeskRow(desk: s.desk, children: sidebar.deskSessions.count)
                .modifier(ScriptedShade(id: "row:desk", model: model, item: .desk))
                .tag(SidebarItem.desk)
            ForEach(sidebar.deskSessions) { item in sessionRows(item, now: now) }
            ForEach(sidebar.sections) { section in
                if section.mode == "parked" {
                    Section(isExpanded: $parkedOpen) {
                        rows(section, s, now: now)
                    } header: {
                        HStack {
                            Text(section.title)
                            Spacer()
                            Text("\(section.tracks.count)").foregroundStyle(.tertiary)
                        }
                        .padding(.trailing, Space.m)
                    }
                } else {
                    Section(section.title) { rows(section, s, now: now) }
                }
            }
        }
        .listStyle(.sidebar)
        .animation(model.windowVisible ? Motion.moves(Motion.quick, reduce: reduce) : nil, value: sidebar.structure)
        // The usage limits at the sidebar's foot (EMI-408): outside the list's scrolling, gone with the sidebar.
        .safeAreaBar(edge: .bottom, spacing: Space.none) { SidebarFoot(store: store) }
        // Always there, its count dimmed at 0: a bar that comes and goes changes the list's top inset after the first layout,
        // and the rows then rest 1 to 3 px apart from one launch to the next (EMI-486).
        .safeAreaBar(edge: .top, spacing: Space.none) {
            WaitingHeader(count: sidebar.waitingOnYou)
        }
        .onChange(of: model.foldRequest) { _, request in
            guard let id = request?.target.split(separator: ":", maxSplits: 1).last, request?.target.hasPrefix("track:") == true,
                  let row = sidebar.sections.flatMap(\.tracks).first(where: { $0.id == String(id) }) else { return }
            toggle(row)
        }
        .onChange(of: model.selection) { _, item in
            model.pickedQuestion = nil
            model.questionDismissed = false
            if let session = model.session(in: store.snapshot) {
                Task { await store.opened(session.id) }
            }
            _ = item
        }
    }

    private func toggle(_ row: SidebarTrack) {
        withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { folds.toggle(row) }
    }

    @ViewBuilder
    private func rows(_ section: SidebarSection, _ s: Snapshot, now: Date) -> some View {
        ForEach(section.tracks) { row in
            let expanded = folds.isExpanded(row)
            TrackRow(row: row, expanded: expanded, now: now) { toggle(row) }
            .modifier(ScriptedShade(id: "row:track:\(row.id)", model: model, item: .track(row.id)))
            .tag(SidebarItem.track(row.id))
            .contextMenu { TrackMenu(store: store, row: row) }
            if expanded {
                ForEach(Array(row.nested.enumerated()), id: \.element.id) { i, item in
                    sessionRows(item, now: now, lane: Theme.lane(of: row.id), track: row.track.name, last: i == row.nested.count - 1)
                }
            }
        }
    }

    /// A session's row with its subagents one level further in, joined to its track by tree lines in the lane colour.
    @ViewBuilder
    private func sessionRows(_ item: SidebarSession, now: Date, lane: Color? = nil, track: String? = nil, last: Bool = true) -> some View {
        SessionRow(item: item, now: now, lane: lane, track: track)
            .padding(.leading, Theme.childIndent)
            .modifier(TreeLines(color: lane, place: TreePlace(depth: 1, last: last, hasChildren: !item.subagents.isEmpty)))
            // After the indent, so the shade spans the row as the list's highlight does.
            .modifier(ScriptedShade(id: "row:session:\(item.id)", model: model, item: .session(item.id)))
            .tag(SidebarItem.session(item.id))
            .contextMenu { SessionMenu(store: store, session: item.session) }
        ForEach(Array(item.subagents.enumerated()), id: \.offset) { i, agent in
            SubagentRow(agent: agent, lane: lane)
                .padding(.leading, Theme.childIndent * 2)
                .modifier(TreeLines(color: lane, place: TreePlace(depth: 2, last: i == item.subagents.count - 1, trunkContinues: !last)))
                .selectionDisabled()
        }
    }
}

struct DeskRow: View {
    var desk: DeskInfo
    var children: Int

    var body: some View {
        HStack(spacing: Space.s) {
            Image(systemName: "bubble.left")
                .foregroundStyle(.tint)
                .frame(width: Space.l)
            Text("Prism").fontWeight(.medium)
            Spacer(minLength: Space.xs)
            if let queued = desk.queuedEvents, queued > 0 {
                Text("\(queued) queued").font(.prismMeta).foregroundStyle(.secondary)
            } else if desk.status == "working" {
                Spinner(color: .accentColor)
            } else {
                Text(desk.status ?? "").font(.prismMeta).foregroundStyle(.secondary)
            }
        }
    }
}

struct TrackRow: View {
    var row: SidebarTrack
    var expanded: Bool
    var now: Date
    var toggle: () -> Void

    var body: some View {
        let chip = row.chip(expanded: expanded)
        let lane = Theme.lane(Lane.of(row.id))
        // A track whose one session has stopped, and that waits on nobody, reads quieter than one at work.
        let quiet = row.nested.isEmpty && [.stopped, .idle].contains(row.primary?.state) && row.track.waitingOnPerson == nil
        HStack(spacing: Space.s) {
            LaneChip(color: lane, height: row.line == nil ? Theme.laneChip.height : Theme.laneChipTall)
                .frame(width: Space.l)
            // The name always shows; the second line says what the track does, waits on or last said (Compact).
            VStack(alignment: .leading, spacing: Space.xxs) {
                Text(row.track.name)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .foregroundStyle(quiet ? .secondary : .primary)
                if let line = row.line {
                    Text(line)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            .layoutPriority(1)
            Spacer(minLength: Space.xs)
            ChipView(chip: chip, mine: !expanded && row.ownedByEmil && row.primary != nil, now: now, lane: lane)
                .fixedSize()
            if !row.nested.isEmpty {
                Button(action: toggle) {
                    Image(systemName: "chevron.right")
                        .font(.prismMeta.weight(.semibold))
                        .foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        .iconTarget()
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide sessions" : "Show sessions")
            }
        }
        .padding(.vertical, row.line == nil ? 0 : Space.xxs)
        .opacity(row.track.shown == "waiting" || row.track.shown == "parked" ? 0.7 : 1)
        .help([row.track.name, row.line].compactMap { $0 }.joined(separator: "\n"))
    }
}

struct SessionRow: View {
    var item: SidebarSession
    var now: Date
    var lane: Color? = nil
    /// The track's name, which the row leaves out of its own (EMI-405).
    var track: String? = nil

    var body: some View {
        let session = item.session
        VStack(alignment: .leading, spacing: Space.xxs) {
            HStack(spacing: Space.s) {
                ChipIcon(chip: item.chip, lane: lane)
                    .frame(width: Space.l)
                Text(item.title(under: track))
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .fontWeight(session.isUnseenDone ? .semibold : .regular)
                Spacer(minLength: Space.xs)
                if session.isEmils {
                    Image(systemName: "person.fill").font(.prismMeta).foregroundStyle(.tertiary).help("Yours")
                }
                // The state is never the part that is cut: the name gives way first.
                ChipLabel(chip: item.chip, now: now, updated: session.updatedAt)
                    .fixedSize()
            }
            if let activity = item.activityLine(under: track) {
                Text(activity)
                    .font(.prismMeta)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .truncationMode(.tail)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, Space.l + Space.s)
            }
        }
        .help([item.title(under: track), item.activityLine(under: track)].compactMap { $0 }.joined(separator: "\n"))
        .opacity(session.state == .idle || session.state == .stopped ? 0.6 : 1)
    }
}

/// A subagent of a session: a spinner while it works, a hollow dot once done.
struct SubagentRow: View {
    var agent: Subagent
    var lane: Color? = nil

    var body: some View {
        HStack(spacing: Space.s) {
            Group {
                if agent.isWorking { Spinner(color: lane) } else { Dot(color: .secondary, hollow: true) }
            }
            .frame(width: Space.l)
            Text(agent.name)
                .font(.callout)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: Space.xs)
            if !agent.isWorking { Text("done").font(.prismMeta).foregroundStyle(.secondary) }
        }
        .opacity(agent.isWorking ? 1 : 0.6)
        .help(agent.name)
    }
}

/// The leading mark of a session row: spinner, dot or nothing (chips with words sit at the trailing edge).
struct ChipIcon: View {
    var chip: StatusChip
    /// A working session spins in its track's lane colour.
    var lane: Color? = nil

    @Environment(\.accessibilityReduceMotion) private var reduce
    @Environment(WindowModel.self) private var model: WindowModel?

    var body: some View {
        let shapes = model?.lab.option("marks") == "shapes"
        ZStack {
            switch chip {
            case .working: Spinner(color: lane).transition(Motion.fadeTransition)
            case .doneUnseen where shapes: mark("checkmark", Theme.done).transition(Motion.fadeTransition)
            case .idle where shapes: mark("pause.fill", Theme.queued).transition(Motion.fadeTransition)
            case .question where shapes: mark("diamond.fill", Theme.question).transition(Motion.needsYouTransition(reduce: reduce))
            case .permission where shapes, .error where shapes: mark("diamond.fill", Theme.alert).transition(Motion.needsYouTransition(reduce: reduce))
            case .doneUnseen: Dot(color: Theme.done).transition(Motion.fadeTransition)
            case .done: Dot(color: .secondary, hollow: true).transition(Motion.fadeTransition)
            case .idle: Dot(color: Theme.queued).transition(Motion.fadeTransition)
            case .stopped: Dot(color: .secondary, hollow: true).transition(Motion.fadeTransition)
            case .question: Dot(color: Theme.question).transition(Motion.needsYouTransition(reduce: reduce))
            case .permission, .error: Dot(color: Theme.alert).transition(Motion.needsYouTransition(reduce: reduce))
            default: Color.clear.frame(width: Theme.dotSize, height: Theme.dotSize)
            }
        }
        .animation(Motion.fade, value: chip)
    }
}

extension ChipIcon {
    /// EMI-484 prototype: a state's own shape, at the dot's weight.
    fileprivate func mark(_ symbol: String, _ color: Color) -> some View {
        Image(systemName: symbol).font(.caption2.weight(.bold)).foregroundStyle(color).frame(width: Theme.spinnerSize, height: Theme.spinnerSize)
    }
}

/// The trailing words of a session row.
struct ChipLabel: View {
    var chip: StatusChip
    var now: Date
    var updated: Date?

    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        Group {
            switch chip {
            case .question: Pill(text: "Question", color: Theme.question).transition(Motion.needsYouTransition(reduce: reduce))
            case .permission: Pill(text: "Permission", color: Theme.alert).transition(Motion.needsYouTransition(reduce: reduce))
            case .error: Pill(text: "Error", color: Theme.alert).transition(Motion.needsYouTransition(reduce: reduce))
            case .working: meta(Theme.ago(updated, now: now))
            case .doneUnseen, .done: meta("done")
            case .idle: meta("idle")
            case .stopped: meta("stopped")
            // A short name ("Alex · 3h") fits beside the row's name; a longer note goes on the row's second line.
            // Who it waits on is the row's second line; the corner says for how long.
            case .waitingOn(_, let since): meta(Theme.ago(since, now: now))
            case .sessions(let n): meta("\(n)")
            case .none: EmptyView()
            }
        }
        .animation(Motion.fade, value: chip)
    }

    private func meta(_ s: String) -> some View {
        Text(s).font(.prismMeta).foregroundStyle(.secondary).monospacedDigit()
    }
}

/// A track row's chip; Emil's own tracks say so.
struct ChipView: View {
    var chip: StatusChip
    var mine: Bool
    var now: Date
    var lane: Color? = nil

    var body: some View {
        switch chip {
        case .question, .permission, .error, .waitingOn, .sessions, .none:
            ChipLabel(chip: chip, now: now)
        case .working:
            HStack(spacing: Space.xs) {
                if mine { Text("yours").font(.prismMeta).foregroundStyle(.secondary) }
                Spinner(color: lane)
            }
        default:
            HStack(spacing: Space.xs) {
                if case .doneUnseen = chip { Dot(color: Theme.done) }
                Text(mine ? "yours · \(word)" : word).font(.prismMeta).foregroundStyle(.secondary)
            }
        }
    }

    private var word: String {
        switch chip {
        case .doneUnseen, .done: "done"
        case .idle: "idle"
        case .stopped: "stopped"
        default: ""
        }
    }
}

struct SessionMenu: View {
    var store: DeskStore
    var session: Session

    var body: some View {
        if session.isEmils {
            Button("Give Back to Prism") { Task { await store.giveBack(session.id) } }
        } else {
            Button("Take Over in Rex") { Task { await store.takeOver(session.id) } }
        }
        if session.state != .stopped {
            Button("Stop Session") { Task { await store.stop(session.id) } }
        }
    }
}

struct TrackMenu: View {
    var store: DeskStore
    var row: SidebarTrack

    var body: some View {
        if let s = row.primary {
            SessionMenu(store: store, session: s)
            Divider()
        }
        Button("Open in Rex") { Task { await store.open(row.id) } }
        Menu("Mode") {
            ForEach(Sidebar.modes, id: \.mode) { mode in
                Button(mode.title) { Task { await store.setMode(row.id, mode.mode) } }
                    .disabled(mode.mode == row.track.mode)
            }
        }
    }
}

/// The sidebar's one count of what waits on Emil: questions, desk decisions and blocked sessions.
struct WaitingHeader: View {
    var count: Int

    var body: some View {
        HStack(spacing: Space.s) {
            Text("Waiting on you").font(.callout.weight(.medium))
            Spacer(minLength: Space.s)
            Text("\(count)")
                .font(.callout.monospacedDigit().weight(.semibold))
                .foregroundStyle(count > 0 ? AnyShapeStyle(Theme.question) : AnyShapeStyle(.tertiary))
        }
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.s)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Waiting on you: \(count)")
    }
}

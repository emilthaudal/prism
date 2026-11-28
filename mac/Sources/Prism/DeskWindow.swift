import DeskCore
import SwiftUI

/// The one window: sessions | conversation | board.
struct DeskWindow: View {
    var store: DeskStore
    var model: WindowModel
    var fixture: Bool

    var body: some View {
        @Bindable var model = model
        let board = store.board
        NavigationSplitView {
            SidebarView(store: store, model: model)
                .navigationSplitViewColumnWidth(min: Theme.sidebarMinWidth, ideal: Theme.sidebarIdealWidth(names: store.snapshot.tracks.map(\.name)), max: Theme.sidebarMaxWidth)
        } detail: {
            MiddleColumn(store: store, model: model)
                // The conversation sits on paper over the wall; the wall runs on under the sidebar's and board's glass.
                .background { PrismWall().ignoresSafeArea().overlay(Theme.paper.ignoresSafeArea()).backgroundExtensionEffect() }
                .inspector(isPresented: $model.boardShown) {
                    BoardView(store: store, model: model, board: board)
                        .background { PrismWall().ignoresSafeArea().overlay(Theme.glass.ignoresSafeArea()) }
                        .inspectorColumnWidth(min: Theme.boardMinWidth, ideal: Theme.boardIdealWidth, max: 460)
                }
        }
        .navigationTitle(title)
        .navigationSubtitle(subtitle(board))
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    model.boardShown.toggle()
                } label: {
                    Label("Board", systemImage: "sidebar.right")
                }
                .help("Show or hide the board")
            }
        }
        .sheet(isPresented: $model.overnightShown) { OvernightSheet(store: store, model: model) }
        .toolbarBackgroundVisibility(.hidden, for: .windowToolbar)
        .frame(minWidth: 900, minHeight: 560)
        .onGeometryChange(for: CGSize.self) { $0.size } action: { windowSize = $0 }
        .environment(\.windowSize, windowSize)
        .environment(model)
    }

    @State private var windowSize: CGSize = .zero

    private var title: String {
        guard let s = model.session(in: store.snapshot) else {
            if case .track(let id) = model.selection, let t = store.snapshot.track(id) { return t.name }
            return "Prism"
        }
        return s.displayName
    }

    private func subtitle(_ board: Board) -> String {
        var parts: [String] = []
        if model.session(in: store.snapshot) == nil, case .track = model.selection {
            parts.append("no session")
        } else if let s = model.session(in: store.snapshot) {
            parts.append(s.isEmils ? "yours" : "Prism's")
            parts.append(s.state.rawValue)
        } else {
            parts.append(board.summary)
        }
        if fixture { parts.append("fixture") }
        if case .offline = store.connection { parts.append("offline") }
        return parts.joined(separator: " · ")
    }
}

/// The desk's chat, or a selected session's transcript.
struct MiddleColumn: View {
    var store: DeskStore
    var model: WindowModel
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        VStack(spacing: Space.none) {
            if model.noticeLabShown { NoticeLabPicker(model: model) }
            if case .offline(let why) = store.connection {
                ConnectionBanner(text: why)
            }
            if case .startFailed(let why) = store.connection {
                ContentUnavailableView {
                    Label("Can't start Prism server", systemImage: "bolt.horizontal.circle")
                } description: {
                    Text(why)
                } actions: {
                    Button("Retry") { store.retry() }
                }
            } else if !store.loaded {
                if case .offline(let why) = store.connection {
                    ContentUnavailableView("Can't reach Prism server", systemImage: "bolt.horizontal.circle", description: Text("\(why)\nStart it with prism server; Prism keeps trying."))
                } else {
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            } else if let session = model.session(in: store.snapshot) {
                notice
                TranscriptView(store: store, model: model, session: session)
            } else if case .track(let id) = model.selection, let track = store.snapshot.track(id) {
                notice
                TrackSummaryView(track: track, now: store.now)
            } else {
                if reduce {
                    // Nothing eases, so the composer is the chat's bottom inset: a height jump moves the messages in
                    // the same layout pass. Below it the chat follows the composer's height frame by frame, which at a
                    // jump showed one frame with the composer changed and the messages not yet.
                    ChatView(store: store, model: model)
                        .dropReceiver { model.attach($0) }
                        .safeAreaInset(edge: .bottom, spacing: Space.none) { composer }
                        .scrollEdgeEffectStyle(.soft, for: .bottom)
                } else {
                    ChatView(store: store, model: model)
                        .dropReceiver { model.attach($0) }
                    // In the column's flow right above the composer, never over it: the composer grows
                    // with a question's options, and an overlay at a fixed height covered them.
                    composer.layoutPriority(1)
                }
            }
        }
        .animation(Motion.fade, value: store.notice)
        .confirmationDialog("Focus is full", isPresented: Binding(get: { store.focusSwap != nil }, set: { if !$0 { store.cancelFocusSwap() } }), presenting: store.focusSwap) { swap in
            ForEach(swap.focus) { t in
                Button("Move \(t.name) to Background") { Task { await store.confirmFocusSwap(leaving: t.name) } }
            }
        } message: { swap in
            Text("Which track makes room for \(swap.trackName)?")
        }
    }
}

extension MiddleColumn {
    private var composer: some View {
        VStack(spacing: Space.none) {
            notice
            ComposerView(store: store, model: model)
                .dropReceiver { model.attach($0) }
        }
    }

    @ViewBuilder var notice: some View {
        if let notice = store.notice, !shownInCard(notice) {
            Group {
                if !notice.isQuiet {
                    NoticeView(notice: notice) { store.notice = nil }
                } else if model.noticeStyle == .banner {
                    NoticeBanner(notice: notice) { store.notice = nil }
                } else {
                    NoticeToast(notice: notice) { store.notice = nil }
                }
            }
            .padding(.horizontal, Space.l)
            .padding(.top, Space.s)
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }

    /// In the card style a notice about the decision on the composer's card is a line in that card instead.
    private func shownInCard(_ notice: Notice) -> Bool {
        model.noticeStyle == .card && notice.isQuiet && notice.questionID != nil && notice.questionID == model.keyedQuestion(store)?.id
    }
}

struct ConnectionBanner: View {
    var text: String

    var body: some View {
        Label("\(text) Retrying\u{2026}", systemImage: "exclamationmark.triangle.fill")
            .font(.callout)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Space.l)
            .padding(.vertical, Space.s)
            .background(Color.orange.opacity(0.10))
    }
}

/// A short message over the conversation: an error, or the command to run to take a session over.
struct NoticeView: View {
    var notice: Notice
    var dismiss: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Image(systemName: notice.isError ? "exclamationmark.triangle.fill" : (notice.symbol ?? "terminal"))
                .foregroundStyle(notice.isError ? Color.orange : Color.secondary)
            VStack(alignment: .leading, spacing: Space.xs) {
                Text(notice.text)
                if let command = notice.command {
                    Text(command).font(.callout.monospaced()).textSelection(.enabled)
                }
            }
            Spacer(minLength: Space.s)
            if let command = notice.command {
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(command, forType: .string)
                }
                .controlSize(.small)
            }
            Button(action: dismiss) { Image(systemName: "xmark").iconTarget() }
                .buttonStyle(.borderless)
                .controlSize(.small)
                .help("Dismiss")
        }
        .font(.callout)
        .padding(Space.m)
        .frame(maxWidth: Theme.chatMaxWidth)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
    }
}

/// A track with no session: what it is for and what it waits on.
struct TrackSummaryView: View {
    var track: Track
    var now: Date

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Space.m) {
                Text(track.name).font(.prismTitle)
                if let goal = track.goal, !goal.isEmpty { Text(goal).foregroundStyle(.secondary) }
                Grid(alignment: .leading, horizontalSpacing: Space.l, verticalSpacing: Space.s) {
                    row("Mode", track.mode.capitalized)
                    if let w = track.waitingOnPerson { row("Waiting on", "\(w), \(Theme.ago(track.updatedAt, now: now))") }
                    if let c = track.check { row("Check", c.ranAt.map { "\(c.summary), \(Theme.ago($0, now: now))" } ?? c.summary) }
                    if let w = track.where, !w.isEmpty { row("Where", w) }
                    if let t = track.today, !t.isEmpty { row("Today", t) }
                    if let tier = track.tier { row("Tier", tier) }
                }
                .font(.callout)
                Text("No session yet. Ask Prism to start one.")
                    .font(.callout)
                    .foregroundStyle(.tertiary)
            }
            .padding(Space.xxl)
            .frame(maxWidth: Theme.chatMaxWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
    }

    private func row(_ label: String, _ value: String) -> some View {
        GridRow {
            Text(label).foregroundStyle(.secondary)
            Text(value)
        }
    }
}

import SwiftUI

/// How the rows of a list that animates arrivals come and go. Rows in no `fresh` id appear as they are.
struct Arrivals: Equatable {
    /// How a fresh row fades in; nil while nothing may animate.
    var fade: Animation?
    /// The ids that arrived, or changed section, in the latest change.
    var fresh: Set<String> = []
    /// How a row fades out as it leaves; nil leaves it to vanish.
    var leaving: Animation?
    /// How far below its place a fresh row starts as it fades in; 0 fades it in place (Reduce Motion).
    var rise: CGFloat = 0
}

extension EnvironmentValues {
    @Entry var arrivals = Arrivals()
}

extension View {
    /// A row that fades in when it is fresh, coming up into its place where the list gives it a rise, and out when it leaves, if the list around it says so. Opacity is
    /// its own animation, so under Reduce Motion the layout can jump while the row still fades.
    func arrives(_ id: String) -> some View { modifier(Arrives(id: id)) }
}

private struct Arrives: ViewModifier {
    var id: String
    @Environment(\.arrivals) private var arrivals
    @State private var shown = false

    func body(content: Content) -> some View {
        let hidden = arrivals.fade != nil && arrivals.fresh.contains(id) && !shown
        content
            .offset(y: hidden ? arrivals.rise : 0)
            .animation(arrivals.rise > 0 ? Motion.arrive : nil, value: hidden)
            .opacity(hidden ? 0 : 1)
            .animation(arrivals.fade, value: hidden)
            .onAppear {
                // One turn later, so the row has been drawn at 0 before it animates to 1.
                if arrivals.fade != nil, arrivals.fresh.contains(id) { DispatchQueue.main.async { shown = true } }
            }
            .transition(arrivals.leaving.map { .asymmetric(insertion: .identity, removal: .opacity.animation($0)) } ?? .identity)
    }
}

/// How a jump settles: one farther than `run` entries skips without animation to `run` short of its target and settles
/// over the rest, since an animated scroll lays out every row it passes.
private enum Settle {
    static let run = 4
    static let skipFrame = 1.0 / 60
}

/// A conversation's scroll view. A new entry comes up into its place as it fades in while the view follows to the bottom, both only when the
/// list had entries before, Emil is at the bottom and the window is on screen. Otherwise entries are inserted without
/// animation, and the view stays where Emil left it.
struct ArrivalScroll<Content: View>: View {
    /// The ids of the entries, oldest first.
    var ids: [String]
    var scrollRequest: WindowModel.ScrollRequest?
    var windowVisible: Bool
    /// An entry to bring to the top, and a note when the view leaves or comes back to the bottom.
    var jumpRequest: WindowModel.JumpRequest? = nil
    var pinnedChanged: (Bool) -> Void = { _ in }
    @ViewBuilder var content: Content

    @Environment(\.accessibilityReduceMotion) private var reduce
    /// The ids as of the last change, and what that change added: a lazy row realized after it still fades in.
    @State private var before: [String] = []
    @State private var recent: Set<String> = []
    @State private var pinned = true
    @State private var userScrolling = false
    @State private var position = ScrollPosition()
    /// The first entry on screen, so a jump knows which side it comes from.
    @State private var topVisible: String?

    var body: some View {
        let animate = windowVisible && pinned && !before.isEmpty
        ScrollView {
            content.environment(\.arrivals, Arrivals(fade: animate ? Motion.fade : nil, fresh: Set(ids).subtracting(before).union(recent), rise: reduce ? 0 : Space.l))
        }
        .scrollPosition($position)
        .defaultScrollAnchor(.bottom, for: .initialOffset)
        .defaultScrollAnchor(.top, for: .sizeChanges)
        // Only Emil scrolling moves the pin: the follow itself, and content that grew, leave the view short of the bottom for a moment.
        .onScrollPhaseChange { _, phase in userScrolling = phase == .interacting || phase == .decelerating }
        .onScrollGeometryChange(for: CGFloat.self) { $0.contentSize.height - $0.visibleRect.maxY } action: { _, gap in
            if userScrolling { pinned = gap <= Space.s }
        }
        // The composer under it growing or shrinking moves the bottom edge: a view at the bottom stays there with it, frame by frame.
        .onScrollGeometryChange(for: CGFloat.self) { $0.containerSize.height } action: { _, _ in
            if pinned, !userScrolling { position.scrollTo(edge: .bottom) }
        }
        .onAppear { before = ids }
        .onChange(of: ids) { old, new in
            if pinned {
                if animate, !reduce { withAnimation(Motion.arrive) { position.scrollTo(edge: .bottom) } } else { position.scrollTo(edge: .bottom) }
            }
            // Only a change that animates leaves its rows fresh. A transcript's first load comes in while `before` is
            // empty, so it doesn't animate and its rows appear at once; marked fresh, they went to opacity 0 as soon as
            // `before` filled, after their onAppear had passed, and the whole transcript stayed blank.
            recent = animate ? Set(new).subtracting(old) : []
            before = new
        }
        .onScrollTargetVisibilityChange(idType: String.self) { topVisible = $0.first }
        .onChange(of: jumpRequest) { _, request in
            guard let request else { return }
            let from = pinned ? ids.count - 1 : topVisible.flatMap { ids.firstIndex(of: $0) }
            pinned = false
            settle(from: from, to: ids.firstIndex(of: request.entry)) { position.scrollTo(id: request.entry, anchor: .top) }
        }
        .onChange(of: pinned) { _, now in pinnedChanged(now) }
        .onChange(of: scrollRequest) { _, request in
            switch request?.direction {
            case "up":
                pinned = false
                position.scrollTo(edge: .top)
            case "bottom":
                pinned = true
                position.scrollTo(edge: .bottom)
            case "latest":
                let from = pinned ? ids.count - 1 : topVisible.flatMap { ids.firstIndex(of: $0) }
                pinned = true
                settle(from: from, to: ids.count - 1) { position.scrollTo(edge: .bottom) }
            default: break
            }
        }
    }

    /// Animates `scroll` with the settle token, first skipping to within `Settle.run` entries of `to` when it is farther
    /// from `from` than that. An unknown `from` counts as the bottom, where the conversation usually is.
    private func settle(from: Int?, to: Int?, _ scroll: @escaping () -> Void) {
        let animation = Motion.moves(Motion.settle, reduce: reduce)
        guard let to, !ids.isEmpty else { return withAnimation(animation, scroll) }
        let from = from ?? ids.count - 1
        guard abs(to - from) > Settle.run else { return withAnimation(animation, scroll) }
        let near = from > to ? min(to + Settle.run, ids.count - 1) : max(to - Settle.run, 0)
        position.scrollTo(id: ids[near], anchor: .top)
        // The skip has to be drawn before the settle starts from it; the next turn of the run loop is too soon.
        DispatchQueue.main.asyncAfter(deadline: .now() + Settle.skipFrame) { withAnimation(animation, scroll) }
    }
}

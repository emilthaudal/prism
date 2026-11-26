import SwiftUI

/// How the rows of a list that animates arrivals come and go. Rows in no `fresh` id appear as they are.
struct Arrivals: Equatable {
    /// How a fresh row fades in; nil while nothing may animate.
    var fade: Animation?
    /// The ids that arrived, or changed section, in the latest change.
    var fresh: Set<String> = []
    /// How a row fades out as it leaves; nil leaves it to vanish.
    var leaving: Animation?
}

extension EnvironmentValues {
    @Entry var arrivals = Arrivals()
}

extension View {
    /// A row that fades in place when it is fresh, and out when it leaves, if the list around it says so. Opacity is
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
            .opacity(hidden ? 0 : 1)
            .animation(arrivals.fade, value: hidden)
            .onAppear {
                // One turn later, so the row has been drawn at 0 before it animates to 1.
                if arrivals.fade != nil, arrivals.fresh.contains(id) { DispatchQueue.main.async { shown = true } }
            }
            .transition(arrivals.leaving.map { .asymmetric(insertion: .identity, removal: .opacity.animation($0)) } ?? .identity)
    }
}

/// A conversation's scroll view. A new entry fades in place while the view follows to the bottom, both only when the
/// list had entries before, Emil is at the bottom and the window is on screen. Otherwise entries are inserted without
/// animation, and the view stays where Emil left it.
struct ArrivalScroll<Content: View>: View {
    /// The ids of the entries, oldest first.
    var ids: [String]
    var scrollRequest: WindowModel.ScrollRequest?
    var windowVisible: Bool
    @ViewBuilder var content: Content

    @Environment(\.accessibilityReduceMotion) private var reduce
    /// The ids as of the last change, and what that change added: a lazy row realized after it still fades in.
    @State private var before: [String] = []
    @State private var recent: Set<String> = []
    @State private var pinned = true
    @State private var userScrolling = false
    @State private var position = ScrollPosition()

    var body: some View {
        let animate = windowVisible && pinned && !before.isEmpty
        ScrollView {
            content.environment(\.arrivals, Arrivals(fade: animate ? Motion.fade : nil, fresh: Set(ids).subtracting(before).union(recent)))
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
            recent = Set(new).subtracting(old)
            before = new
        }
        .onChange(of: scrollRequest) { _, request in
            switch request?.direction {
            case "up":
                pinned = false
                position.scrollTo(edge: .top)
            case "bottom":
                pinned = true
                position.scrollTo(edge: .bottom)
            default: break
            }
        }
    }
}

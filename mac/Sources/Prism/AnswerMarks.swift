import DeskCore
import SwiftUI

// Ways back to an answer of Prism's that started no track: a mark per message of Emil's in the conversation's margin,
// and a Kept section on the board for the answers he keeps.

/// A bar per message of Emil's along the conversation's trailing edge, each where its message sits in the scroll; bars
/// closer than one bar's height are one, which names and goes to the newest of them. The bar of a message on screen is
/// full ink and longer; the pointer on a bar names the message, a click goes to it.
struct AskedRail: View {
    var entries: [ConversationEntry]
    var model: WindowModel

    var body: some View {
        GeometryReader { geo in
            // Clear of the title above and the last line's rise below.
            let top = Space.xxl * 2
            let span = max(geo.size.height - top - Space.xl, 1)
            let byID = Dictionary(entries.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
            ForEach(MarkGroups.group(entries, span: span, minGap: Self.markHeight)) { group in
                if let entry = byID[group.target] {
                    RailMark(entry: entry, ids: group.ids, model: model, height: Self.markHeight)
                        .frame(width: geo.size.width, alignment: .trailing)
                        .position(x: geo.size.width / 2, y: top + group.y)
                }
            }
        }
        // In the column's margin, beside the bubbles' edge.
        .padding(.trailing, Space.xxs)
    }

    private static let markHeight = Theme.iconTarget + Space.xs
}

/// One bar of the rail. Its own view, so a message scrolling on or off screen redraws the bars and not the grouping.
private struct RailMark: View {
    var entry: ConversationEntry
    var ids: [String]
    var model: WindowModel
    var height: CGFloat
    @State private var pointerOver = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        let on = pointerOver || model.hoveredTarget == "mark:\(entry.id)"
        let here = ids.contains { model.askedVisible.contains($0) }
        Button {
            model.askedCursor = entry.id
            model.jump(to: entry.id)
        } label: {
            HStack(spacing: Space.s) {
                if on {
                    HStack(spacing: Space.s) {
                        if let at = entry.at { Text(at.formatted(Theme.time)).font(.prismMeta).foregroundStyle(.secondary) }
                        Text(Theme.markdown(entry.text)).font(.callout).lineLimit(1)
                    }
                    .padding(.horizontal, Space.m)
                    .frame(height: Theme.iconTarget + Space.xs)
                    .frame(maxWidth: Theme.proseMaxWidth / 2)
                    .fixedSize()
                    .glassEffect(.regular, in: Capsule())
                    .transition(Motion.fadeTransition)
                }
                Capsule()
                    .fill(Theme.ink.opacity(on || here ? 1 : 0.3))
                    .frame(width: on ? Space.l : (here ? Space.m : Space.s), height: Theme.laneChip.width)
                    .frame(width: Space.l, alignment: .trailing)
            }
            .frame(height: height)
            .padding(.leading, Space.xs)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { pointerOver = $0 && model.pointerLive }
        .animation(Motion.moves(Motion.quick, reduce: reduce), value: on)
        .animation(Motion.moves(Motion.quick, reduce: reduce), value: here)
        .accessibilityLabel("Go to: \(entry.text)")
    }
}

/// Shown while the conversation is scrolled up from its newest entry: a glass button above the composer that goes back
/// to it, and hides once there. ⌘↓ does the same.
struct ToBottomButton: View {
    var model: WindowModel
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        ZStack {
            if model.scrolledUp {
                Button { model.scroll("latest") } label: {
                    Image(systemName: "arrow.down")
                        .font(.callout.weight(.semibold))
                        .frame(width: Space.xxl, height: Space.xxl)
                        .contentShape(Circle())
                }
                .buttonStyle(.plain)
                .glassEffect(.regular.interactive(), in: Circle())
                .transition(Motion.fadeTransition)
                .help("Back to the newest message (\u{2318}\u{2193})")
                .accessibilityLabel("Go to the newest message")
            }
        }
        .animation(Motion.fade, value: model.scrolledUp)
    }
}

/// Tells the rail which of Emil's messages are on screen.
struct AskedVisibility: ViewModifier {
    var id: String
    var model: WindowModel

    func body(content: Content) -> some View {
        content.onScrollVisibilityChange(threshold: 0.5) { visible in
            if visible { model.askedVisible.insert(id) } else { model.askedVisible.remove(id) }
        }
    }
}

/// A message of Prism's a jump landed on is shaded for a moment, the shade a pressed row has. With keeping on, a bar in
/// its margin holds it on the board: faint under the pointer, ink once kept.
struct AnswerMarks: ViewModifier {
    var id: String
    var model: WindowModel
    @State private var pointerOver = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    func body(content: Content) -> some View {
        let landed = model.landed == id
        let kept = model.kept.contains(id)
        let pointed = pointerOver || model.hoveredTarget == "keep:\(id)"
        content
            .background {
                RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
                    .fill(Color.primary.opacity(landed ? 0.07 : 0))
                    .padding(.vertical, -Space.s)
                    .animation(Motion.fades(Motion.settle, reduce: reduce), value: landed)
            }
            .overlay(alignment: .topLeading) {
                Button { model.toggleKept(id) } label: {
                    LaneChip(color: Theme.ink.opacity(kept ? 1 : (pointed ? 0.3 : 0)))
                        .frame(width: Space.l, height: Theme.iconTarget)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .animation(Motion.fade, value: kept)
                .animation(Motion.fade, value: pointed)
                .help(kept ? "Let go: take this off the board" : "Keep this on the board")
                .accessibilityLabel(kept ? "Let go" : "Keep")
            }
            .onHover { pointerOver = $0 && model.pointerLive }
    }
}

/// The board's way back to what Prism answered directly: the messages Emil kept, newest kept first.
struct KeptSection: View {
    var store: DeskStore
    var model: WindowModel

    var body: some View {
        let rows = Array(Answers.kept(model.kept, in: store.snapshot.conversation).reversed())
        if !rows.isEmpty {
            SectionHeading("Kept", count: rows.count)
            BoardGroup {
                ForEach(rows) { row in
                    DirectAnswerRow(row: row, model: model).arrives(row.id)
                }
            }
        }
    }
}

struct DirectAnswerRow: View {
    var row: DirectAnswer
    var model: WindowModel

    var body: some View {
        Button {
            // From a session's transcript, the conversation has to be on screen before it can scroll.
            let away = model.selection != .desk && model.selection != nil
            model.selection = .desk
            if away {
                Task {
                    try? await Task.sleep(for: .milliseconds(80))
                    model.jump(to: row.anchor, landing: row.id)
                }
            } else {
                model.jump(to: row.anchor, landing: row.id)
            }
        } label: {
            VStack(alignment: .leading, spacing: Space.xs) {
                HStack(spacing: Space.s) {
                    // Ink where a track's row has its lane colour: this came from Emil and went to no track.
                    LaneChip(color: Theme.ink)
                    Text(Theme.markdown(row.question?.text ?? "Prism")).font(.callout.weight(.semibold)).lineLimit(1)
                    Spacer(minLength: Space.s)
                    if let at = row.answer.at { Text(at.formatted(Theme.time)).font(.prismMeta).foregroundStyle(.secondary).fixedSize() }
                }
                Text(Theme.markdown(row.gist))
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(Theme.boardCardPadding)
        }
        .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius, card: true, informs: true))
        .targetID("answer:\(row.id)")
        .contextMenu {
            Button("Let Go") { model.toggleKept(row.id) }
        }
        .help("Go to this answer in the conversation")
    }
}

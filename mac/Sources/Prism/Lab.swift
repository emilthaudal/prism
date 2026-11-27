import DeskCore
import SwiftUI

/// EMI-488 prototypes, not shipped: ways back to an answer of Prism's that started no track, run in the real app on a
/// fixture. Launch with --lab (or --lab asked=marks,answers=listed) and step each choice with its Option key. Every
/// choice's first option is the build as it is, and without --lab nothing here is reached.
struct Lab: Equatable {
    struct Choice {
        /// Its Option key.
        var number: Int
        var surface: String
        var key: String
        var title: String
        var options: [String]
        /// What to look for, per option.
        var notes: [String]
    }

    static let choices: [Choice] = [
        Choice(number: 1, surface: "Conversation", key: "asked", title: "Your questions", options: ["today", "marks"], notes: [
            "Today: your own messages are ink bubbles in the scroll, and the scroll is the only way back to one.",
            "A bar per message of yours along the conversation's edge, placed where it sits in the scroll. Click one to go there; \u{2325}\u{2191} and \u{2325}\u{2193} step between them.",
        ]),
        Choice(number: 2, surface: "Board", key: "answers", title: "Answers", options: ["today", "listed", "kept"], notes: [
            "Today: the board holds decisions, reports and tracks. An answer that started no track is only in the scroll.",
            "Answers, under Reports: every reply to a question of yours, newest first. Click a row to go to it.",
            "Kept, under Reports: only what you keep. Point at a message of Prism's and click the bar in its margin; click a row to go to it.",
        ]),
    ]

    var picks = Array(repeating: 0, count: Lab.choices.count)

    func option(_ key: String) -> String {
        guard let i = Lab.choices.firstIndex(where: { $0.key == key }) else { return "" }
        return Lab.choices[i].options[picks[i]]
    }

    mutating func step(_ i: Int) {
        guard Lab.choices.indices.contains(i) else { return }
        picks[i] = (picks[i] + 1) % Lab.choices[i].options.count
    }

    init() {}

    /// `asked=marks,answers=kept`; a bare `on` keeps every choice at today's.
    init(_ spec: String) throws {
        for part in spec.split(separator: ",") where part != "on" {
            let pair = part.split(separator: "=", maxSplits: 1).map(String.init)
            guard pair.count == 2, let i = Lab.choices.firstIndex(where: { $0.key == pair[0] }), let o = Lab.choices[i].options.firstIndex(of: pair[1]) else {
                throw LaunchOptions.Invalid(description: "--lab takes \(Lab.choices.map { "\($0.key)=\($0.options.joined(separator: "|"))" }.joined(separator: ",")): \(part)")
            }
            picks[i] = o
        }
    }
}

/// The strip over the conversation while a --lab launch is on: each choice, its Option key and what the current option changes.
struct LabPicker: View {
    var model: WindowModel
    @State private var last = 0

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            // One row per surface, so every choice shows whole.
            ForEach(["Conversation", "Board"], id: \.self) { surface in
                HStack(spacing: Space.xs) {
                    Text(surface).font(.prismMeta).foregroundStyle(.tertiary).frame(width: Space.xxl * 3, alignment: .leading).padding(.leading, Space.s)
                    ForEach(Array(Lab.choices.enumerated()).filter { $0.element.surface == surface }, id: \.offset) { i, choice in
                        Button {
                            model.lab.step(i)
                            last = i
                        } label: {
                            HStack(spacing: Space.xs) {
                                Text("\u{2325}\(choice.number)").font(.prismMono).foregroundStyle(.tertiary)
                                Text(choice.title).font(.prismMeta)
                                Text(choice.options[model.lab.picks[i]].capitalized).font(.prismMeta.weight(.semibold))
                            }
                            .fixedSize()
                            .padding(.horizontal, Space.s)
                            .frame(height: Theme.iconTarget)
                        }
                        .buttonStyle(RowButtonStyle(fill: model.lab.picks[i] == 0 ? .clear : Color.primary.opacity(0.09)))
                    }
                }
            }
            Text(Lab.choices[last].notes[model.lab.picks[last]]).font(.prismMeta).foregroundStyle(.secondary).padding(.horizontal, Space.s)
        }
        .frame(maxWidth: Theme.chatMaxWidth, alignment: .leading)
        .frame(maxWidth: .infinity)
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.xs)
        .onAppear { last = model.lab.picks.firstIndex { $0 != 0 } ?? 0 }
        .onChange(of: model.lab) { old, new in
            if let i = old.picks.indices.first(where: { old.picks[$0] != new.picks[$0] }) { last = i }
        }
    }
}

// MARK: Conversation: marks

/// A bar per message of Emil's along the conversation's trailing edge, each where its message sits in the scroll. The
/// bar of a message on screen is full ink and longer; the pointer on a bar names the message, a click goes to it.
struct AskedRail: View {
    var entries: [ConversationEntry]
    var model: WindowModel
    @State private var pointed: String?
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        GeometryReader { geo in
            // Clear of the title above and the last line's rise below.
            let top = Space.xxl * 2
            let span = max(geo.size.height - top - Space.xl, 1)
            ForEach(Array(entries.enumerated()).filter { $0.element.role == "emil" }, id: \.element.id) { i, entry in
                mark(entry)
                    .frame(width: geo.size.width, alignment: .trailing)
                    .position(x: geo.size.width / 2, y: top + span * (CGFloat(i) + 0.5) / CGFloat(entries.count))
            }
        }
        // In the column's margin, beside the bubbles' edge.
        .padding(.trailing, Space.xxs)
    }

    private func mark(_ entry: ConversationEntry) -> some View {
        let on = pointed == entry.id || model.hoveredTarget == "mark:\(entry.id)"
        let here = model.askedVisible.contains(entry.id)
        return Button {
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
            .frame(height: Theme.iconTarget + Space.xs)
            .padding(.leading, Space.xs)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { over in
            if over, model.pointerLive { pointed = entry.id } else if pointed == entry.id { pointed = nil }
        }
        .animation(Motion.moves(Motion.quick, reduce: reduce), value: on)
        .animation(Motion.moves(Motion.quick, reduce: reduce), value: here)
        .accessibilityLabel("Go to: \(entry.text)")
    }
}

/// Tells the rail which of Emil's messages are on screen.
struct AskedVisibility: ViewModifier {
    var id: String
    var model: WindowModel
    var on: Bool

    func body(content: Content) -> some View {
        if on {
            content.onScrollVisibilityChange(threshold: 0.5) { visible in
                if visible { model.askedVisible.insert(id) } else { model.askedVisible.remove(id) }
            }
        } else {
            content
        }
    }
}

// MARK: Conversation: landing and keeping

/// A message of Prism's a jump landed on is shaded for a moment, the shade a pressed row has. With keeping on, a bar in
/// its margin holds it on the board: faint under the pointer, ink once kept.
struct AnswerMarks: ViewModifier {
    var id: String
    var model: WindowModel
    var keeps: Bool
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
                if keeps {
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
            }
            .onHover { pointerOver = $0 && keeps && model.pointerLive }
    }
}

// MARK: Board

/// The board's way back to what Prism answered directly: every reply to a question of Emil's, or only what he kept.
struct AnswersSection: View {
    var store: DeskStore
    var model: WindowModel

    var body: some View {
        let mode = model.lab.option("answers")
        let conversation = store.snapshot.conversation
        let rows = mode == "kept" ? Array(Answers.kept(model.kept, in: conversation).reversed()) : (mode == "listed" ? Array(Answers.direct(conversation).reversed()) : [])
        if !rows.isEmpty {
            SectionHeading(mode == "kept" ? "Kept" : "Answers", count: rows.count)
            BoardGroup {
                ForEach(rows) { row in
                    DirectAnswerRow(row: row, model: model, kept: mode == "kept").arrives(row.id)
                }
            }
        }
    }
}

struct DirectAnswerRow: View {
    var row: DirectAnswer
    var model: WindowModel
    var kept: Bool

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
            if kept { Button("Let Go") { model.toggleKept(row.id) } }
        }
        .help("Go to this answer in the conversation")
    }
}

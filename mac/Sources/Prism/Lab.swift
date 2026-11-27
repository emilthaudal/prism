import DeskCore
import SwiftUI

/// EMI-477 prototypes, not shipped: options for one surface at a time, run in the real app on a fixture. Launch with
/// --lab (or --lab reports=plain,cards=ranked) and step each choice with its Option key. Every choice's first option is
/// the build as it is, and without --lab nothing here is reached.
struct Lab: Equatable {
    struct Choice {
        /// Its Option key, kept as choices are picked and removed, so the design page's numbers stay true.
        var number: Int
        var surface: String
        var key: String
        var title: String
        var options: [String]
        /// What to look for, per option.
        var notes: [String]
    }

    static let choices: [Choice] = [
        Choice(number: 1, surface: "Conversation", key: "reports", title: "Reports", options: ["card", "plain", "glass"], notes: [
            "Today: a painted card, 4 % fill and a hairline.",
            "No box. A report is text on the paper like Prism's own, told apart by its dot and name; glass is left to what you act on.",
            "Each report is a piece of glass. It scrolls under the composer's glass.",
        ]),
        Choice(number: 3, surface: "Conversation", key: "arrive", title: "Arrivals", options: ["fade", "rise"], notes: [
            "Today: a new entry fades in where it lands.",
            "A new entry comes up from the composer's edge as it fades in; your own message grows out of the field's corner.",
        ]),
        Choice(number: 5, surface: "Sidebar", key: "marks", title: "Status marks", options: ["dots", "shapes"], notes: [
            "Today: finished, idle, a question and blocked are dots of one shape, told apart by colour.",
            "Each state has its own shape, drawn like the dot: a ring once seen, a short bar for idle, the board's diamond for what needs you.",
        ]),
        Choice(number: 8, surface: "Sidebar", key: "tree", title: "Session lines", options: ["elbows", "quiet", "rail"], notes: [
            "Today: a trunk and an elbow into each session's mark, as heavy as the marks.",
            "The same tree in a hairline at half strength, joined to the lane chip and stopping short of each mark.",
            "No tree. The track's lane chip runs on as one bar beside its sessions; the indent says the rest.",
        ]),
        Choice(number: 6, surface: "Sidebar", key: "usage", title: "Usage", options: ["two lines", "one line"], notes: [
            "Today: each limit is a bar with its reset time on a line below.",
            "Each limit is one line; the reset time takes the percentage's place under the pointer.",
        ]),
        Choice(number: 7, surface: "Board", key: "cards", title: "Board cards", options: ["mixed", "even", "ranked", "lined", "grouped"], notes: [
            "Today: decisions and reports have a fill and a hairline, tracks and loops a fill alone.",
            "One card for everything on the board: the fill and the hairline.",
            "Only a decision is a card. Reports, tracks and loops are rows on the board's glass and shade under the pointer.",
            "Ranked, with body: each section's rows sit close with a hairline between them, and a decision's card is lifted to a lighter fill.",
            "Ranked, with body: each section's rows share one soft sheet with hairlines between them, and a decision's card is lifted to a lighter fill.",
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

    /// `reports=plain,cards=ranked`; a bare `on` keeps every choice at today's.
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

/// EMI-480 prototype: a board section's rows. Under "lined" and "grouped" they sit close with hairlines between them, and
/// "grouped" puts one soft sheet under the section; otherwise each is laid out on its own as before.
struct BoardGroup<Content: View>: View {
    @ViewBuilder var content: Content
    @Environment(WindowModel.self) private var model: WindowModel?

    var body: some View {
        let look = model?.lab.option("cards") ?? "mixed"
        if look == "lined" || look == "grouped" {
            let shape = RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous)
            VStack(alignment: .leading, spacing: Space.none) {
                Group(subviews: content) { rows in
                    ForEach(Array(rows.enumerated()), id: \.element.id) { i, row in
                        if i > 0 { Rectangle().fill(Theme.hairline).frame(height: 1).padding(.horizontal, Theme.boardCardPadding) }
                        row
                    }
                }
            }
            .background(look == "grouped" ? Theme.cardFill : .clear, in: shape)
            .clipShape(shape)
        } else {
            VStack(alignment: .leading, spacing: Space.m) { content }
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
            ForEach(["Conversation", "Sidebar", "Board"], id: \.self) { surface in
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
        .onChange(of: model.lab) { old, new in
            if let i = old.picks.indices.first(where: { old.picks[$0] != new.picks[$0] }) { last = i }
        }
    }
}

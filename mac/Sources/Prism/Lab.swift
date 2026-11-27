import DeskCore
import SwiftUI

/// EMI-477 prototypes, not shipped: options for one surface at a time, run in the real app on a fixture. Launch with
/// --lab (or --lab reports=plain,owner=glass) and step each choice with its Option key. Every choice's first option is
/// the build as it is, and without --lab nothing here is reached.
struct Lab: Equatable {
    struct Choice {
        var key: String
        var title: String
        var options: [String]
        /// What to look for, per option.
        var notes: [String]
    }

    static let choices: [Choice] = [
        Choice(key: "reports", title: "Reports", options: ["card", "plain", "glass"], notes: [
            "Today: a painted card, 4 % fill and a hairline.",
            "No box. A report is text on the paper like Prism's own, told apart by its dot and name; glass is left to what you act on.",
            "Each report is a piece of glass. It scrolls under the composer's glass.",
        ]),
        Choice(key: "owner", title: "Owner bar", options: ["line", "glass"], notes: [
            "Today: a line of text and a system button on the paper.",
            "One piece of glass where the message field sits, the action in ink like Send. A decision above it is a second piece.",
        ]),
        Choice(key: "arrive", title: "Arrivals", options: ["fade", "rise"], notes: [
            "Today: a new entry fades in where it lands.",
            "A new entry comes up from the composer's edge as it fades in; your own message grows out of the field's corner.",
        ]),
        Choice(key: "edge", title: "Top edge", options: ["cut", "soft"], notes: [
            "Today: the conversation is cut straight under the title.",
            "The conversation thins out under the title over one line's height.",
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

    /// `reports=plain,owner=glass`; a bare `on` keeps every choice at today's.
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

/// The conversation's top edge under the title: cut straight, or thinned out over one line (EMI-483 prototype).
struct TitleEdge: ViewModifier {
    @Environment(WindowModel.self) private var model: WindowModel?

    func body(content: Content) -> some View {
        if model?.lab.option("edge") == "soft" {
            content.mask(VStack(spacing: Space.none) {
                LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: .bottom).frame(height: Space.xxl)
                Rectangle()
            })
        } else {
            content.mask(Rectangle())
        }
    }
}

/// The strip over the conversation while a --lab launch is on: each choice, its Option key and what the current option changes.
struct LabPicker: View {
    var model: WindowModel
    @State private var last = 0

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            HStack(spacing: Space.xs) {
                ForEach(Array(Lab.choices.enumerated()), id: \.offset) { i, choice in
                    Button {
                        model.lab.step(i)
                        last = i
                    } label: {
                        HStack(spacing: Space.xs) {
                            Text("\u{2325}\(i + 1)").font(.prismMono).foregroundStyle(.tertiary)
                            Text(choice.title).font(.prismMeta)
                            Text(choice.options[model.lab.picks[i]].capitalized).font(.prismMeta.weight(.semibold))
                        }
                        .padding(.horizontal, Space.s)
                        .frame(height: Theme.iconTarget)
                    }
                    .buttonStyle(RowButtonStyle(fill: model.lab.picks[i] == 0 ? .clear : Color.primary.opacity(0.09)))
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

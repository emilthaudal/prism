import DeskCore
import SwiftUI

/// EMI-477 prototypes, not shipped: options for one surface at a time, run in the real app on a fixture. Launch with
/// --lab (or --lab marks=shapes) and step each choice with its Option key. Every choice's first option is
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
        Choice(number: 5, surface: "Sidebar", key: "marks", title: "Status marks", options: ["dots", "shapes"], notes: [
            "Today: finished, idle, a question and blocked are dots of one shape, told apart by colour.",
            "Each state has its own shape, drawn like the dot: a ring once seen, a short bar for idle, the board's diamond for what needs you.",
        ]),
        Choice(number: 6, surface: "Sidebar", key: "usage", title: "Usage", options: ["two lines", "one line"], notes: [
            "Today: each limit is a bar with its reset time on a line below.",
            "Each limit is one line; the reset time takes the percentage's place under the pointer.",
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

    /// `marks=shapes`; a bare `on` keeps every choice at today's.
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
            ForEach(["Sidebar"], id: \.self) { surface in
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

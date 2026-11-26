import DeskCore
import SwiftUI

/// The board's Loops section: one card per overnight loop that runs or waits out a pause.
struct LoopsSection: View {
    var store: DeskStore
    var model: WindowModel
    var loops: [LoopBrief]
    var now: Date

    var body: some View {
        if !loops.isEmpty {
            SectionHeading("Loops", count: loops.count)
            ForEach(loops) { brief in
                LoopCard(store: store, brief: brief) { model.selection = .session(brief.session.id) }
                    .arrives(brief.id)
            }
        }
    }
}

/// One loop in the style of a track's brief: its goal, what it does now, its progress and a way to stop it.
struct LoopCard: View {
    var store: DeskStore
    var brief: LoopBrief
    var open: () -> Void
    @State private var stopping = false

    var body: some View {
        Button(action: open) { card }
            .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius))
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                LaneChip(color: Theme.lane(Lane.of(brief.session.track ?? brief.session.id)))
                Text(brief.name).font(.callout.weight(.semibold)).lineLimit(1).truncationMode(.tail).layoutPriority(1)
                Spacer(minLength: Space.xs)
                Text(brief.loop.iteration).font(.prismMeta).foregroundStyle(.secondary).fixedSize()
            }
            if brief.goal != nil || brief.now != nil {
                Grid(alignment: .topLeading, horizontalSpacing: Space.s, verticalSpacing: Space.xxs) {
                    line("Goal", brief.goal)
                    line("Now", brief.now)
                }
            }
            ProgressView(value: brief.loop.fraction).controlSize(.small)
            HStack(spacing: Space.s) {
                if let footer = brief.loop.footer() {
                    Text(footer).font(.prismMeta).foregroundStyle(.secondary).lineLimit(2).truncationMode(.tail)
                }
                Spacer(minLength: Space.xs)
                if let track = brief.session.track {
                    Button(stopping ? "Stopping after this turn" : "Stop after this turn") {
                        stopping = true
                        Task { await store.stopLoop(track) }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .fixedSize()
                    .disabled(stopping)
                }
            }
        }
        .padding(Theme.boardCardPadding)
    }

    @ViewBuilder
    private func line(_ label: String, _ text: String?) -> some View {
        if let text {
            GridRow(alignment: .firstTextBaseline) {
                Text(label).font(.callout).foregroundStyle(.secondary)
                Text(text).font(.callout).lineLimit(2).truncationMode(.tail).frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

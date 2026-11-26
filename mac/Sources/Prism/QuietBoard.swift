import DeskCore
import SwiftUI

/// The board's quiet state: the day in one line, then a brief per open track.
struct QuietBoard: View {
    var plan: BoardPlan
    var model: WindowModel
    var now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            if let line = plan.digestLine {
                Text(line).font(.callout).foregroundStyle(.secondary).boardText()
            }
            if plan.tracksCount > 0 {
                SectionHeading("Tracks", count: plan.tracksCount)
                ForEach(plan.briefs) { brief in
                    TrackBriefCard(brief: brief, now: now) { model.selection = .track(brief.id) }
                }
                ForEach(plan.waiting) { waiting in
                    WaitingTrackRow(waiting: waiting) { model.selection = .track(waiting.id) }
                }
            }
        }
    }
}

/// One open track: what it is for, where it stands, its check and links. Quieter than a decision: no stroke.
struct TrackBriefCard: View {
    var brief: TrackBrief
    var now: Date
    var open: () -> Void

    var body: some View {
        Button(action: open) { card }
            .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius))
    }

    private var card: some View {
        let lane = Theme.lane(Lane.of(brief.id))
        return VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                LaneChip(color: lane)
                Text(brief.track.name).font(.callout.weight(.semibold)).lineLimit(1).truncationMode(.tail).layoutPriority(1)
                Spacer(minLength: Space.xs)
                if case .sessions(let n) = brief.chip {
                    Text("\(n) sessions").font(.prismMeta).foregroundStyle(.secondary).fixedSize()
                } else {
                    ChipView(chip: brief.chip, mine: false, now: now, lane: lane).fixedSize()
                }
            }
            if brief.goal != nil || brief.now != nil || brief.today != nil {
                Grid(alignment: .topLeading, horizontalSpacing: Space.s, verticalSpacing: Space.xxs) {
                    line("Goal", brief.goal)
                    line("Now", brief.now)
                    line("Today", brief.today)
                }
            }
            if let footer {
                Text(footer).font(.prismMeta).foregroundStyle(.secondary).lineLimit(2).truncationMode(.tail).frame(maxWidth: .infinity, alignment: .leading)
            }
            if !brief.links.isEmpty {
                WrapLayout(spacing: Space.s) {
                    ForEach(brief.links) { link in
                        Button { NSWorkspace.shared.open(link.url) } label: { Text(link.title).lineLimit(1) }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                            .help(link.url.absoluteString)
                    }
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

    private var footer: String? {
        let check = brief.check.map { c in
            "Check: " + c.summary + (c.ranAt.map { " · " + Theme.ago($0, now: now) + " ago" } ?? "")
        }
        let person = brief.person.map { "Waits on " + $0 }
        let parts = [check, person].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

/// A waiting track as one line: its name and what it waits on.
struct WaitingTrackRow: View {
    var waiting: WaitingBrief
    var open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(spacing: Space.s) {
                LaneChip(color: Theme.lane(Lane.of(waiting.id)))
                Text(waiting.track.name).font(.body.weight(.medium)).lineLimit(1).truncationMode(.tail).layoutPriority(1)
                if let detail = waiting.detail {
                    Text(detail).font(.subheadline).foregroundStyle(.secondary).lineLimit(1).truncationMode(.tail)
                }
                Spacer(minLength: 0)
            }
            .padding(.vertical, Space.xs)
            .padding(.horizontal, Space.m)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowButtonStyle())
    }
}

/// Buttons side by side that wrap to the next line instead of truncating.
struct WrapLayout: Layout {
    var spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        arrange(width: proposal.width ?? .infinity, subviews).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let placed = arrange(width: bounds.width, subviews)
        for (subview, frame) in zip(subviews, placed.frames) {
            subview.place(at: CGPoint(x: bounds.minX + frame.minX, y: bounds.minY + frame.minY), proposal: ProposedViewSize(frame.size))
        }
    }

    private func arrange(width: CGFloat, _ subviews: Subviews) -> (size: CGSize, frames: [CGRect]) {
        var frames: [CGRect] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0
        var widest: CGFloat = 0
        for subview in subviews {
            let ideal = subview.sizeThatFits(.unspecified)
            let size = subview.sizeThatFits(ProposedViewSize(width: min(ideal.width, width), height: nil))
            if x > 0, x + size.width > width {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            frames.append(CGRect(origin: CGPoint(x: x, y: y), size: size))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
            widest = max(widest, x - spacing)
        }
        return (CGSize(width: widest, height: y + rowHeight), frames)
    }
}

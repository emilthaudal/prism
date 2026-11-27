import DeskCore
import SwiftUI

/// A card per open track with its goal and how far it is. The sidebar says what the track does
/// now; this says where it is going, how much is done and what comes next.
struct MilestoneSection: View {
    var store: DeskStore
    var model: WindowModel
    var now: Date
    /// The cards folded out to their checklist. A click folds a card out and back; it never changes what the
    /// conversation shows, which only the sidebar does (Emil, 27 Nov).
    @State private var opened: Set<String> = []
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        let briefs = BoardPlan.openBriefs(store.snapshot)
        if !briefs.isEmpty {
            SectionHeading("Tracks", count: briefs.count)
            ForEach(briefs) { brief in
                MilestoneCard(brief: brief, open: opened.contains(brief.id), now: now) { toggle(brief.id) }
                    .targetID("milestones:\(brief.id)")
            }
            // A demo script folds a card with `milestones:ID`.
            .onChange(of: model.foldRequest) { _, request in
                if let target = request?.target, target.hasPrefix("milestones:") { toggle(String(target.dropFirst("milestones:".count))) }
            }
        }
    }

    private func toggle(_ id: String) {
        withAnimation(Motion.moves(Motion.quick, reduce: reduce)) {
            if opened.contains(id) { opened.remove(id) } else { opened.insert(id) }
        }
    }
}

struct MilestoneCard: View {
    var brief: TrackBrief
    /// Open shows every milestone, the check and the links; closed shows the rail and the next one.
    var open: Bool
    var now: Date
    var toggle: () -> Void

    private var steps: [Milestone] { brief.track.milestones ?? [] }

    var body: some View {
        Button(action: toggle) { card }
            .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius))
    }

    private var card: some View {
        let lane = Theme.lane(Lane.of(brief.id))
        return VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                LaneChip(color: lane)
                Text(brief.track.name).font(.callout.weight(.semibold)).lineLimit(1).truncationMode(.tail).layoutPriority(1)
                Spacer(minLength: Space.xs)
                if !steps.isEmpty {
                    Text("\(brief.track.milestonesDone) of \(steps.count)").font(.prismMeta).foregroundStyle(.secondary).monospacedDigit().fixedSize()
                }
            }
            if let goal = brief.goal {
                Text(goal).font(.callout).foregroundStyle(.secondary).lineLimit(2).truncationMode(.tail).frame(maxWidth: .infinity, alignment: .leading)
            }
            if steps.isEmpty {
                // No milestones written: today's plan is the nearest thing to "how far".
                if let today = brief.today { labelled("Today", today) }
            } else if open {
                VStack(alignment: .leading, spacing: Space.xs) {
                    ForEach(Array(steps.enumerated()), id: \.offset) { i, step in row(step, next: i == brief.track.milestonesDone, lane: lane) }
                }
                .padding(.top, Space.xxs)
            } else {
                MilestoneRail(steps: steps, lane: lane).padding(.vertical, Space.xxs)
                if let next = brief.track.nextMilestone { labelled("Next", next.title) } else { labelled("Done", steps.last?.title ?? "") }
            }
            if open {
                if let check = brief.check {
                    Text("Check: " + check.summary + (check.ranAt.map { " · " + Theme.ago($0, now: now) + " ago" } ?? ""))
                        .font(.prismMeta).foregroundStyle(.secondary).lineLimit(2)
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
        }
        .padding(Theme.boardCardPadding)
    }

    private func labelled(_ label: String, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(label).font(.callout).foregroundStyle(.secondary)
            Text(text).font(.callout.weight(.medium)).lineLimit(1).truncationMode(.tail)
            Spacer(minLength: 0)
        }
    }

    private func row(_ step: Milestone, next: Bool, lane: Color) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Image(systemName: step.done ? "checkmark.circle.fill" : next ? "circle.circle" : "circle")
                .font(.callout)
                .foregroundStyle(step.done || next ? AnyShapeStyle(lane) : AnyShapeStyle(.tertiary))
            Text(step.title)
                .font(next ? .callout.weight(.semibold) : .callout)
                .foregroundStyle(step.done ? .secondary : .primary)
                .lineLimit(1)
                .truncationMode(.tail)
            if next { Text("next").font(.prismMeta).foregroundStyle(.secondary) }
            Spacer(minLength: 0)
        }
    }
}

/// The milestones as a line of dots in the track's colour: filled when reached, a ring for the next, faint after it.
struct MilestoneRail: View {
    var steps: [Milestone]
    var lane: Color
    private static let dot: CGFloat = 8

    var body: some View {
        let done = steps.filter(\.done).count
        HStack(spacing: Space.xxs) {
            ForEach(Array(steps.enumerated()), id: \.offset) { i, _ in
                if i > 0 {
                    Capsule().fill(i <= done ? AnyShapeStyle(lane.opacity(i < done ? 1 : 0.45)) : AnyShapeStyle(.quaternary)).frame(height: 2)
                }
                Group {
                    if i < done {
                        Circle().fill(lane)
                    } else if i == done {
                        Circle().strokeBorder(lane, lineWidth: 2)
                    } else {
                        Circle().fill(.quaternary)
                    }
                }
                .frame(width: Self.dot, height: Self.dot)
            }
        }
        .accessibilityElement()
        .accessibilityLabel("\(done) of \(steps.count) milestones reached")
    }
}

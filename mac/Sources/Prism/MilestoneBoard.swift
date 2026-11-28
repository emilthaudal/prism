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
            BoardGroup {
                ForEach(briefs) { brief in
                    MilestoneCard(brief: brief, open: opened.contains(brief.id), now: now) { toggle(brief.id) }
                        .targetID("milestones:\(brief.id)")
                }
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
    /// The rail's dots and the checklist's marks are the same marks: folding out moves each dot to its line.
    @Namespace private var marks

    var body: some View {
        Button(action: toggle) { card }
            .buttonStyle(RowButtonStyle(fill: Theme.cardFill, radius: Theme.cardRadius, informs: true))
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
                    ForEach(Array(steps.enumerated()), id: \.offset) { i, step in row(i, step, next: i == brief.track.milestonesDone, lane: lane) }
                }
                .padding(.top, Space.xxs)
            } else {
                MilestoneRail(steps: steps, lane: lane, marks: marks).padding(.vertical, Space.xxs)
                if let next = brief.track.nextMilestone {
                    HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                        Text("Next").font(.callout).foregroundStyle(.secondary)
                        nextTitle(next.title)
                        Spacer(minLength: 0)
                    }
                } else {
                    labelled("Done", steps.last?.title ?? "")
                }
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
        // While the card grows, the checklist shows only as far as the card has got.
        .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
    }

    /// The next milestone's name, the same text closed ("Next Merged") and in the checklist: it moves, it is not redrawn.
    private func nextTitle(_ title: String) -> some View {
        Text(title).font(.callout.weight(.semibold)).lineLimit(1).truncationMode(.tail)
            .matchedGeometryEffect(id: "next", in: marks, properties: .position, anchor: .leading)
    }

    private func labelled(_ label: String, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(label).font(.callout).foregroundStyle(.secondary)
            Text(text).font(.callout.weight(.medium)).lineLimit(1).truncationMode(.tail)
            Spacer(minLength: 0)
        }
    }

    private func row(_ i: Int, _ step: Milestone, next: Bool, lane: Color) -> some View {
        HStack(spacing: Space.s) {
            MilestoneMark(state: step.done ? .reached : next ? .next : .later, lane: lane, size: MilestoneMark.listed)
                .matchedGeometryEffect(id: i, in: marks)
            if next {
                nextTitle(step.title)
            } else {
                Text(step.title).font(.callout).foregroundStyle(step.done ? .secondary : .primary).lineLimit(1).truncationMode(.tail)
            }
            if next { Text("next").font(.prismMeta).foregroundStyle(.secondary) }
            Spacer(minLength: 0)
            if let pr = step.pr {
                Text([pr, step.prState].compactMap { $0 }.joined(separator: " · ")).font(.prismMeta).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
        }
    }
}

/// One milestone's mark, the same in the rail and in the checklist: filled in the track's colour when reached (with a
/// tick once it is large enough to hold one), a ring for the next, faint after it.
struct MilestoneMark: View {
    enum State { case reached, next, later }
    var state: State
    var lane: Color
    var size: CGFloat
    static let dot: CGFloat = 8
    static let listed: CGFloat = 14

    var body: some View {
        Group {
            switch state {
            case .reached:
                Circle().fill(lane).overlay {
                    Image(systemName: "checkmark")
                        .font(.system(size: size * 0.5, weight: .bold))
                        .foregroundStyle(.white)
                        .opacity(size >= Self.listed ? 1 : 0)
                }
            case .next: Circle().strokeBorder(lane, lineWidth: 2)
            // A faint dot on the rail; in the list a hairline ring, so an open step doesn't read as a grey blob.
            case .later:
                if size >= Self.listed { Circle().strokeBorder(.tertiary, lineWidth: 1.5) } else { Circle().fill(.quaternary) }
            }
        }
        .frame(width: size, height: size)
    }
}

/// The milestones as a line of dots in the track's colour.
struct MilestoneRail: View {
    var steps: [Milestone]
    var lane: Color
    var marks: Namespace.ID

    var body: some View {
        let done = steps.filter(\.done).count
        HStack(spacing: Space.xxs) {
            ForEach(Array(steps.enumerated()), id: \.offset) { i, _ in
                if i > 0 {
                    Capsule().fill(i <= done ? AnyShapeStyle(lane.opacity(i < done ? 1 : 0.45)) : AnyShapeStyle(.quaternary)).frame(height: 2)
                }
                MilestoneMark(state: i < done ? .reached : i == done ? .next : .later, lane: lane, size: MilestoneMark.dot)
                    .matchedGeometryEffect(id: i, in: marks)
            }
        }
        .accessibilityElement()
        .accessibilityLabel("\(done) of \(steps.count) milestones reached")
    }
}

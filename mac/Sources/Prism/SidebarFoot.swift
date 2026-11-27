import DeskCore
import SwiftUI

/// The foot of the left sidebar (EMI-408, placement 3): the Claude 5-hour and weekly limits as two one-line bars, when
/// they reset, and what is running. It lives in the sidebar, so it goes when the sidebar is collapsed. Design:
/// docs/design/prism-identity.html, "A status bar for Prism".
struct SidebarFoot: View {
    var store: DeskStore

    var body: some View {
        // Reset times and "as of" age move with the clock, not only with the snapshot.
        TimelineView(.periodic(from: .now, by: 30)) { _ in
            content(now: store.now)
        }
    }

    private func content(now: Date) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            usage(now: now)
        }
        .font(.prismMeta)
        .foregroundStyle(.secondary)
        .lineLimit(1)
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .overlay(alignment: .top) { Rectangle().fill(Theme.hairline).frame(height: 0.5) }
    }

    @ViewBuilder private func usage(now: Date) -> some View {
        if let u = store.snapshot.desk.usage, u.fiveHour != nil || u.sevenDay != nil {
            let stale = u.isStale(now: now)
            VStack(alignment: .leading, spacing: Space.xs) {
                if let l = u.fiveHour { UsageMeter(label: "5h", limit: l, now: now, dimmed: stale) }
                if let l = u.sevenDay { UsageMeter(label: "Week", limit: l, now: now, dimmed: stale) }
                if stale, let at = u.updatedAt {
                    Text("as of \(Theme.ago(at, now: now)) ago")
                }
            }
            .help(stale ? "No Claude session has drawn its status line since then, so these may be behind." : "From your Claude status line.")
        } else {
            Text("Usage shows once a Claude session has run")
        }
    }
}

/// One limit as one line: label, a small bar that fills the width and the percentage; under the pointer the percentage
/// gives way to when the limit resets. At the limit, "Limit reached" and when work can resume.
struct UsageMeter: View {
    var label: String
    var limit: UsageLimit
    var now: Date
    var dimmed: Bool
    @State private var hovering = false

    var body: some View {
        let level = limit.level(now: now)
        let pct = limit.shownPercentage(now: now)
        let reset = limit.resetsAt.flatMap { $0 > now ? Self.when($0, now: now) : nil }
        HStack(spacing: Space.s) {
            Text(label).foregroundStyle(.primary.opacity(0.75)).fontWeight(.medium)
                .frame(width: Theme.usageLabelWidth, alignment: .leading)
            if level == .reached {
                Text(reset.map { "Limit reached, back \($0)" } ?? "Limit reached").fontWeight(.semibold).foregroundStyle(color(level))
                Spacer(minLength: 0)
            } else {
                Capsule().fill(Theme.hairline)
                    .frame(height: 4)
                    .overlay(alignment: .leading) {
                        GeometryReader { g in
                            Capsule().fill(color(level)).frame(width: g.size.width * CGFloat(pct) / 100)
                        }
                    }
                Group {
                    if hovering, let reset { Text("resets \(reset)") } else {
                        Text("\(pct)%").monospacedDigit().fontWeight(.semibold)
                            .foregroundStyle(level == .plenty ? AnyShapeStyle(.primary) : AnyShapeStyle(color(level)))
                    }
                }
                .fixedSize()
            }
        }
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .animation(Motion.fade, value: hovering)
        .opacity(dimmed ? 0.55 : 1)
        .accessibilityElement(children: .combine)
    }

    private func color(_ level: UsageLimit.Level) -> Color {
        switch level {
        case .plenty: Color.primary.opacity(0.6)
        case .close: Theme.warn
        case .nearlyOut, .reached: Theme.question
        }
    }

    /// "16:40" within a day, else "Mon 09:00".
    static func when(_ date: Date, now: Date) -> String {
        if date.timeIntervalSince(now) < 20 * 3600 { return date.formatted(Theme.time) }
        return date.formatted(.dateTime.weekday(.abbreviated).hour().minute().locale(Theme.locale))
    }
}

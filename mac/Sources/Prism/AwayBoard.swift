import AppKit
import DeskCore
import SwiftUI

/// Feeds the window's key and the app's active state to `model.away`, which keeps when Emil last looked.
@MainActor
final class AwayObserver {
    private var tokens: [any NSObjectProtocol] = []

    init(model: WindowModel, window: NSWindow, now: @escaping () -> Date) {
        let center = NotificationCenter.default
        func watch(_ name: Notification.Name, object: AnyObject?, _ change: @escaping @MainActor () -> Void) {
            tokens.append(center.addObserver(forName: name, object: object, queue: .main) { _ in MainActor.assumeIsolated { change() } })
        }
        watch(NSWindow.didBecomeKeyNotification, object: window) { model.away.windowBecameKey(now: now()) }
        // A digest read for a minute leaves with the fade the board gives a card that goes.
        let resign: @MainActor () -> Void = {
            withAnimation(Motion.quick) { model.away.windowResigned(now: now()) }
        }
        watch(NSWindow.didResignKeyNotification, object: window, resign)
        watch(NSApplication.didResignActiveNotification, object: nil, resign)
        watch(NSApplication.willTerminateNotification, object: nil) { model.away.windowResigned(now: now()) }
        // A launch counts as coming in front, so a window that is already key (or a capture) still shows the digest.
        model.away.windowBecameKey(now: now())
    }
}

/// "While you were away": what happened since Emil last had the window in front.
struct AwaySection: View {
    var digest: AwayDigest
    var model: WindowModel
    var now: Date
    @State private var all = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                SectionHeading(digest.heading, detail: digest.awayLine)
                Spacer(minLength: Space.s)
                Button("Got it") {
                    withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { model.away.dismiss(now: now) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .targetID("away:dismiss")
            }
            if !digest.rows.isEmpty { summary }
            let shown = all ? digest.timeline : digest.shown
            VStack(alignment: .leading, spacing: Space.none) {
                ForEach(shown) { entry in AwayTimelineRow(entry: entry, model: model) }
                if digest.hidden > 0 {
                    Button(all ? "Show fewer" : "Show all \(digest.timeline.count)") {
                        withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { all.toggle() }
                    }
                    .buttonStyle(.plain)
                    .font(.callout.weight(.medium))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, Space.m)
                    .padding(.top, Space.xs)
                }
            }
        }
    }

    private var summary: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            ForEach(digest.rows, id: \.title) { row in
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    Text(row.title).font(.callout)
                    Spacer(minLength: Space.s)
                    if let trailing = row.trailing { Text(trailing).font(.prismMeta).foregroundStyle(.secondary) }
                }
            }
        }
        .padding(Theme.boardCardPadding)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).fill(Theme.cardFill))
        .overlay(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).strokeBorder(Theme.hairline))
    }
}

/// One thing that happened while Emil was away: its time, what it was and, for a session with changes, the diff.
struct AwayTimelineRow: View {
    var entry: AwayEntry
    var model: WindowModel

    var body: some View {
        if let target {
            Button { model.selection = target } label: { row }.buttonStyle(RowButtonStyle())
        } else {
            row
        }
    }

    private var target: SidebarItem? {
        if let session = entry.session { return .session(session) }
        return entry.track.map { .track($0) }
    }

    private var row: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(Away.clock(entry.at)).font(.prismMeta).foregroundStyle(.secondary).frame(width: Space.xxl + Space.l, alignment: .leading)
            VStack(alignment: .leading, spacing: Space.xxs) {
                Text(entry.title).font(.body.weight(.medium)).lineLimit(1).truncationMode(.tail)
                if let detail = entry.detail {
                    Text(detail).font(.subheadline).foregroundStyle(.secondary).lineLimit(2).truncationMode(.tail)
                }
            }
            Spacer(minLength: Space.s)
            if let changes = entry.changes { DiffStat(changes: changes) }
        }
        .padding(.vertical, Space.xs)
        .padding(.horizontal, Space.m)
        .contentShape(Rectangle())
        .help([entry.title, entry.detail].compactMap { $0 }.joined(separator: "\n"))
    }
}

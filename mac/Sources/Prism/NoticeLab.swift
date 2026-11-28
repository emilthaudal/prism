import DeskCore
import SwiftUI

/// EMI-500 prototypes, not shipped: how a quiet notice such as "Asked pantry-api about D19" shows. Launch on a fixture
/// with --lab notice=banner|toast|card (make lab) and step the choice with ⌥1. Without --lab the banner shows.
enum NoticeStyle: String, CaseIterable {
    /// A slim glass row over the composer, a dot for its kind, closed with its X or on its own.
    case banner
    /// A small glass capsule centred over the composer, no close button; it leaves on its own.
    case toast
    /// A notice about a question is a line in that question's card while the card is loaded; others are toasts.
    case card

    var title: String {
        switch self {
        case .banner: "Banner"
        case .toast: "Toast"
        case .card: "In the card"
        }
    }

    var note: String {
        switch self {
        case .banner: "A slim glass row above the composer with a dot and an X. Leaves after 5 s, or when its question is answered."
        case .toast: "A small capsule centred above the composer, no X. Leaves after 5 s, or when its question is answered."
        case .card: "Inside the decision's card, under its question, while that card is loaded; other notices are toasts."
        }
    }

    var next: NoticeStyle {
        let all = Self.allCases
        return all[(all.firstIndex(of: self)! + 1) % all.count]
    }

    init(lab spec: String) throws {
        let pair = spec.split(separator: "=", maxSplits: 1).map(String.init)
        if spec == "on" {
            self = .banner
        } else if pair.count == 2, pair[0] == "notice", let style = NoticeStyle(rawValue: pair[1]) {
            self = style
        } else {
            throw LaunchOptions.Invalid(description: "--lab takes notice=\(Self.allCases.map(\.rawValue).joined(separator: "|")): \(spec)")
        }
    }
}

/// The strip over the conversation while a --lab launch is on: the choice, its key and what the current option is.
struct NoticeLabPicker: View {
    var model: WindowModel

    var body: some View {
        HStack(spacing: Space.s) {
            Button { model.noticeStyle = model.noticeStyle.next } label: {
                HStack(spacing: Space.xs) {
                    Text("\u{2325}1").font(.prismMono).foregroundStyle(.tertiary)
                    Text("Asked notice").font(.prismMeta)
                    Text(model.noticeStyle.title).font(.prismMeta.weight(.semibold))
                }
                .fixedSize()
                .padding(.horizontal, Space.s)
                .frame(height: Theme.iconTarget)
            }
            .buttonStyle(RowButtonStyle(fill: Color.primary.opacity(0.09)))
            Text(model.noticeStyle.note).font(.prismMeta).foregroundStyle(.secondary).lineLimit(2)
        }
        .frame(maxWidth: Theme.chatMaxWidth, alignment: .leading)
        .frame(maxWidth: .infinity)
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.xs)
    }
}

/// A quiet notice's dot: magenta when it is about a decision, as the decision's own marks are.
private struct NoticeDot: View {
    var notice: Notice

    var body: some View {
        Dot(color: notice.questionID != nil ? Theme.question : Color.secondary)
    }
}

/// A quiet notice as a slim glass row: its dot, its text and an X.
struct NoticeBanner: View {
    var notice: Notice
    var dismiss: () -> Void

    var body: some View {
        HStack(spacing: Space.s) {
            NoticeDot(notice: notice)
            Text(notice.text).lineLimit(2)
            Spacer(minLength: Space.s)
            Button(action: dismiss) { Image(systemName: "xmark").iconTarget() }
                .buttonStyle(.borderless)
                .controlSize(.small)
                .help("Dismiss")
        }
        .font(.callout)
        .foregroundStyle(.secondary)
        .padding(.leading, Space.m)
        .padding(.trailing, Space.s)
        .padding(.vertical, Space.xs)
        .frame(maxWidth: Theme.chatMaxWidth)
        .glassEffect(.regular, in: Capsule())
    }
}

/// A quiet notice as a small capsule centred over the composer; a click puts it away early.
struct NoticeToast: View {
    var notice: Notice
    var dismiss: () -> Void

    var body: some View {
        Button(action: dismiss) {
            HStack(spacing: Space.s) {
                NoticeDot(notice: notice)
                Text(notice.text).lineLimit(1)
            }
            .font(.callout)
            .foregroundStyle(.secondary)
            .padding(.horizontal, Space.l)
            .padding(.vertical, Space.s)
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .glassEffect(.regular, in: Capsule())
        .frame(maxWidth: .infinity)
        .help("Dismiss")
    }
}

/// The card's own line while its question was asked about: the hollow dot of something waiting, and who was asked.
struct AskedLine: View {
    var track: String

    var body: some View {
        HStack(spacing: Space.s) {
            Dot(color: Theme.question, hollow: true)
            Text("Asked \(track) about this. Its reply comes into the conversation.")
        }
        .font(.callout)
        .foregroundStyle(.secondary)
        .transition(Motion.fadeTransition)
    }
}

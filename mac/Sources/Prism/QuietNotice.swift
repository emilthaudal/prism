import DeskCore
import SwiftUI

/// A quiet notice such as "Asked pantry-api about D19" (EMI-500, picked 28 Nov): a line in the decision's card while that
/// card is on the composer, otherwise a small capsule over the composer. It leaves after 5 s, or when its question is answered.

/// A quiet notice's dot: magenta when it is about a decision, as the decision's own marks are.
private struct NoticeDot: View {
    var notice: Notice

    var body: some View {
        Dot(color: notice.questionID != nil ? Theme.question : Color.secondary)
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

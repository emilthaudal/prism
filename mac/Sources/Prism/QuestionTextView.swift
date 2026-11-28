import DeskCore
import SwiftUI

/// A question as the composer's card shows it: the title in the question's weight, the rest under it in reading
/// weight with its steps one per line. A long body opens folded, and scrolls when shown whole rather than push the
/// conversation off the window.
struct QuestionTextView: View {
    var question: String
    @State private var whole = QuestionTextView.opensWhole
    @State private var wholeHeight: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduce

    /// Parts a folded body shows, each cut to a couple of lines.
    private static let foldedParts = 2
    private static let foldedLines = 2
    private static let titleLines = 4
    /// Fixture captures only (--demo-whole): a body opens whole, as "Show all" leaves it.
    nonisolated(unsafe) static var opensWhole = false

    var body: some View {
        let text = Theme.questionText(question)
        let folded = text.folds && !whole
        VStack(alignment: .leading, spacing: Space.xs) {
            if !text.title.isEmpty {
                // A height forced to fit made a long title blank the window: the header's HStack measures it at
                // narrow widths. A title is a sentence of at most QuestionText.titleMax characters.
                Text(Theme.questionMarkdown(text.title))
                    .font(.prismQuestion)
                    .lineLimit(Self.titleLines)
            }
            if !text.body.isEmpty {
                if folded {
                    parts(Array(text.body.prefix(Self.foldedParts)), folded: true)
                } else {
                    ScrollView {
                        parts(text.body, folded: false)
                            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { wholeHeight = $0 }
                    }
                    .scrollBounceBehavior(.basedOnSize)
                    .frame(height: min(wholeHeight, Theme.questionBodyMaxHeight))
                }
                if text.folds {
                    Button(whole ? "Show less" : "Show all") {
                        withAnimation(Motion.moves(Motion.quick, reduce: reduce)) { whole.toggle() }
                    }
                    .buttonStyle(.plain)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                }
            }
        }
        .help(question)
    }

    private func parts(_ shown: [QuestionText.Part], folded: Bool) -> some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            ForEach(Array(shown.enumerated()), id: \.offset) { _, part in
                switch part {
                case .text(let s):
                    Text(Theme.questionMarkdown(s))
                        .lineLimit(folded ? Self.foldedLines : nil)
                case .step(let marker, let s):
                    HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                        Text(marker)
                            .font(.prismMono)
                            .foregroundStyle(.secondary)
                            .frame(minWidth: Space.m, alignment: .trailing)
                        Text(Theme.questionMarkdown(s))
                            .lineLimit(folded ? Self.foldedLines : nil)
                    }
                }
            }
        }
        .font(.body)
        .frame(maxWidth: .infinity, alignment: .leading)
        .textSelection(.enabled)
    }
}

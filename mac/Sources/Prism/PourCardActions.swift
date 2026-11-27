import DeskCore
import SwiftUI

/// The pour on a board card: each option is a glass capsule, and the one you pick becomes the countdown pill while the
/// others run into it. The card keeps the height of its options while the pill shows, and the card the composer has
/// loaded keeps its options too, so nothing under a card moves when it is picked or answered. Answering here or in the
/// composer is the same answer: the countdown shows in both and there is no second one to give.
struct PourCardActions: View {
    var store: DeskStore
    var question: Question
    @Namespace private var ns
    @Environment(\.accessibilityReduceMotion) private var reduce

    static func fits(_ q: Question, in store: DeskStore) -> Bool {
        !q.options.isEmpty && store.stagedChoice(for: q.id) == nil && store.answers.batch(containing: q.id) == nil && !store.stagesAnswer(to: q)
    }

    var body: some View {
        let pending = store.answers.pending(for: question.id)
        let picked: Int? = { if let pending, case .option(let n) = pending.choice { return n } else { return nil } }()
        ZStack(alignment: .topLeading) {
            // The options' own layout, unseen, holds the height.
            WrapLayout(spacing: Space.s) {
                ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in label(i, option) }
            }
            .hidden()
            .accessibilityHidden(true)
            GlassEffectContainer(spacing: Space.s) {
                if let pending {
                    HStack(spacing: Space.s) {
                        CountdownLine(deadline: pending.deadline, now: store.now) { "Sending in \($0) s" }
                            .font(.callout)
                            .padding(.horizontal, Space.m)
                            .frame(height: Space.xxl)
                            .glassEffect(.regular, in: Capsule())
                            .glassEffectID("option\(picked ?? 0)", in: ns)
                        Spacer(minLength: Space.s)
                        Button { store.undo(question.id) } label: {
                            HStack(spacing: Space.s) { Text("Undo"); KeyHint(text: "z") }
                                .font(.callout)
                                .padding(.horizontal, Space.m)
                                .frame(height: Space.xxl)
                                .contentShape(Capsule())
                        }
                        .buttonStyle(.plain)
                        .glassEffect(.regular.interactive(), in: Capsule())
                        .glassEffectID("undo", in: ns)
                    }
                } else {
                    WrapLayout(spacing: Space.s) {
                        ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                            Button { store.answer(question.id, choice: .option(i)) } label: { label(i, option).contentShape(Capsule()) }
                                .buttonStyle(.plain)
                                .help(option.description ?? "")
                                .glassEffect(.regular.interactive(), in: Capsule())
                                .glassEffectID("option\(i)", in: ns)
                        }
                    }
                }
            }
        }
        .animation(Motion.moves(Motion.pour, reduce: reduce), value: pending?.deadline)
    }

    /// Ink-tinted glass read as grey with pale text in light, so the recommended one is marked by its star and weight
    /// and the glass stays clear.
    private func label(_ i: Int, _ option: Option) -> some View {
        let recommended = i == question.recommendedIndex
        return Text(recommended ? "\u{2605} \(option.label)" : option.label)
            .font(.callout.weight(recommended ? .semibold : .regular))
            .lineLimit(1)
            .padding(.horizontal, Space.m)
            .frame(height: Space.xxl)
    }
}

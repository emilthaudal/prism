import DeskCore
import SwiftUI

/// EMI-477 prototype, not shipped: three ways the moment of answering a decision could feel, beside today's. Launch on a
/// fixture with --feel NAME and flip with ⌥1–⌥5. today is the production composer and the default everywhere else.
enum Feel: String, CaseIterable {
    case today, precise, fluid, glass, morph

    var title: String {
        switch self {
        case .today: "Today"
        case .precise: "Precise"
        case .fluid: "Fluid"
        case .glass: "Living glass"
        case .morph: "Glass, morphing"
        }
    }

    /// What to look for, in one line under the picker.
    var note: String {
        switch self {
        case .today: "The build as it is: options make way for a countdown row."
        case .precise: "Nothing moves. The picked option holds, Undo sits where Send was, a digit key shows on its row."
        case .fluid: "The option you pick becomes the countdown. One spring, and it can be undone mid-move."
        case .glass: "One piece of glass that draws in to a pill while it sends. No dropped frames."
        case .morph: "Two pieces of glass that pour into each other. The system's morph drops frames today."
        }
    }

    /// How the composer's height follows an answer.
    var answerMotion: Animation {
        switch self {
        case .today, .precise: Motion.answer
        case .fluid: .spring(response: 0.38, dampingFraction: 0.82)
        case .glass, .morph: .snappy(duration: 0.24)
        }
    }
}

/// The picker over the composer while a prototype launch is on: the four feels and what the current one changes.
struct FeelPicker: View {
    var model: WindowModel

    var body: some View {
        VStack(alignment: .trailing, spacing: Space.xs) {
            HStack(spacing: Space.xs) {
                ForEach(Array(Feel.allCases.enumerated()), id: \.offset) { i, feel in
                    Button { model.feel = feel } label: {
                        HStack(spacing: Space.xs) {
                            Text("\u{2325}\(i + 1)").font(.prismMono).foregroundStyle(.tertiary)
                            Text(feel.title).font(.prismMeta.weight(model.feel == feel ? .semibold : .regular))
                        }
                        .padding(.horizontal, Space.s)
                        .frame(height: Theme.iconTarget)
                    }
                    .buttonStyle(RowButtonStyle(fill: model.feel == feel ? Color.primary.opacity(0.09) : .clear))
                }
            }
            Text(model.feel.note).font(.prismMeta).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .padding(.horizontal, Space.s)
    }
}

/// One decision with options, drawn in the chosen feel. Anything else (a dialog, a batch, pictures, no options) keeps
/// the production composer.
struct FeelComposer: View {
    var store: DeskStore
    var model: WindowModel
    var question: Question
    var position: (Int, Int)?
    var feel: Feel
    var focus: FocusState<ComposerField?>.Binding
    @Namespace private var ns
    @Environment(\.accessibilityReduceMotion) private var reduce

    static func fits(_ q: Question, in store: DeskStore) -> Bool {
        !q.options.isEmpty && !q.isVisualChoice && !store.stagesAnswer(to: q) && store.answers.batch(containing: q.id) == nil
    }

    private var pending: PendingAnswer? { store.answers.pending(for: question.id) }

    private var picked: Int? {
        if let pending, case .option(let n) = pending.choice { return n }
        return nil
    }

    var body: some View {
        Group {
            switch feel {
            case .today: EmptyView()
            case .precise: precise
            case .fluid: fluid
            case .glass: glass
            case .morph: EmptyView()
            }
        }
        .environment(\.keysLive, focus.wrappedValue == nil)
    }

    // MARK: Precise: the composer keeps its size from question to countdown to sent.

    private var precise: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            header.padding(.horizontal, Space.s)
            VStack(spacing: Space.xxs) {
                ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                    let isPicked = picked == i
                    let recommended = i == question.recommendedIndex
                    Button { pick(i) } label: { optionLabel(i, option, mark: isPicked ? "checkmark.circle.fill" : (recommended && pending == nil ? "largecircle.fill.circle" : "circle"), lit: isPicked || (recommended && pending == nil)) }
                        .buttonStyle(RowButtonStyle(fill: isPicked ? Color.primary.opacity(0.09) : (recommended && pending == nil ? Color.accentColor.opacity(0.10) : .clear)))
                        .targetID(Self.target(question.id, i))
                        .opacity(pending != nil && !isPicked ? 0.35 : 1)
                        .allowsHitTesting(pending == nil)
                }
            }
            .animation(Motion.fade, value: picked)
            Divider()
            // One slot: the field and Send, or the clock and Undo, at the same height and with the button in the same place.
            HStack(spacing: Space.s) {
                if let pending {
                    CountdownLine(deadline: pending.deadline, now: store.now) { "Sending in \($0) s" }
                    Spacer(minLength: Space.s)
                    undoButton.buttonStyle(.bordered)
                } else {
                    chip
                    field
                    sendButton.buttonStyle(InkButtonStyle())
                }
            }
            .frame(minHeight: Space.xxl)
            .padding(.horizontal, Space.s)
            .id(pending?.deadline)
            .transition(Motion.fadeTransition)
        }
        .padding(.horizontal, Space.s)
        .padding(.vertical, Space.m)
        .focusOnTap(focus, .answer)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: Theme.composerRadius, style: .continuous))
    }

    // MARK: Fluid: the picked option is the countdown; the rest fold away on one spring.

    private var spring: Animation? { reduce ? nil : Feel.fluid.answerMotion }

    private var fluid: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            header.padding(.horizontal, Space.s)
            if let pending, let i = picked {
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.tint)
                    Text(question.options[i].label)
                        .matchedGeometryEffect(id: "label\(i)", in: ns, properties: .position, anchor: .leading)
                    CountdownLine(deadline: pending.deadline, now: store.now) { "sends in \($0) s" }
                        .foregroundStyle(.secondary)
                        .transition(.opacity)
                    Spacer(minLength: Space.s)
                    undoButton.buttonStyle(.bordered).transition(.opacity)
                }
                .padding(Space.s)
                .background {
                    RoundedRectangle(cornerRadius: Space.s, style: .continuous).fill(Color.accentColor.opacity(0.14))
                        .matchedGeometryEffect(id: "row\(i)", in: ns)
                }
            } else {
                VStack(spacing: Space.xxs) {
                    ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                        let recommended = i == question.recommendedIndex
                        Button { pick(i) } label: {
                            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                                Image(systemName: recommended ? "largecircle.fill.circle" : "circle")
                                    .foregroundStyle(recommended ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                                VStack(alignment: .leading, spacing: Space.xxs) {
                                    Text(option.label)
                                        .matchedGeometryEffect(id: "label\(i)", in: ns, properties: .position, anchor: .leading)
                                    if let d = option.description, !d.isEmpty {
                                        Text(Theme.markdown(d)).font(.callout).foregroundStyle(.secondary)
                                    }
                                }
                                Spacer(minLength: Space.s)
                                if recommended { Text("\u{2605} Recommended").font(.prismMeta).foregroundStyle(.secondary) }
                                KeyHint(text: "\(i + 1)")
                            }
                            .padding(Space.s)
                        }
                        .buttonStyle(GiveButtonStyle(fill: recommended ? Color.accentColor.opacity(0.10) : .clear, id: "row\(i)", ns: ns))
                        .targetID(Self.target(question.id, i))
                    }
                }
                .transition(.opacity)
                VStack(spacing: Space.s) {
                    Divider()
                    HStack(spacing: Space.s) {
                        chip
                        field
                        sendButton.buttonStyle(InkButtonStyle())
                    }
                    .padding(.horizontal, Space.s)
                }
                .transition(.opacity)
            }
        }
        .padding(.horizontal, Space.s)
        .padding(.vertical, Space.m)
        .focusOnTap(focus, .answer)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: Theme.composerRadius, style: .continuous))
        .animation(spring, value: pending?.deadline)
    }

    // MARK: Living glass: the decision and the field are two pieces of glass that pour into each other.

    var decisionContent: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            header.padding(.horizontal, Space.s)
            VStack(spacing: Space.xxs) {
                ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                    let recommended = i == question.recommendedIndex
                    Button { pick(i) } label: { optionLabel(i, option, mark: recommended ? "largecircle.fill.circle" : "circle", lit: recommended) }
                        .buttonStyle(RowButtonStyle(fill: recommended ? Color.accentColor.opacity(0.10) : .clear))
                        .targetID(Self.target(question.id, i))
                }
            }
        }
        .padding(.horizontal, Space.s)
        .padding(.vertical, Space.m)
    }

    func countdownContent(_ pending: PendingAnswer) -> some View {
        CountdownLine(deadline: pending.deadline, now: store.now) { "Sending \u{201C}\(pending.label)\u{201D} in \($0) s" }
            .padding(.horizontal, Space.l)
            .padding(.vertical, Space.s)
    }

    /// The field's row while a decision is loaded: the chip, the field, and Send or, while it counts down, Undo.
    func answerRow(counting: Bool) -> some View {
        HStack(spacing: Space.s) {
            chip
            field
            if counting {
                undoButton.buttonStyle(.bordered)
            } else {
                sendButton.buttonStyle(InkButtonStyle())
            }
        }
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.m)
        .focusOnTap(focus, .answer)
    }

    /// One glass view whose content and size change: no dropped frames (make motion-composer, 27 Nov).
    private var glass: some View {
        let shape = RoundedRectangle(cornerRadius: Theme.composerRadius, style: .continuous)
        return VStack(alignment: .leading, spacing: Space.s) {
            Group {
                if let pending { countdownContent(pending).transition(.opacity) } else { decisionContent.transition(.opacity) }
            }
            .glassEffect(.regular, in: shape)
            answerRow(counting: pending != nil).glassEffect(.regular.interactive(), in: shape)
        }
        .animation(reduce ? nil : feel.answerMotion, value: pending?.deadline)
    }

    // MARK: Shared pieces

    static func target(_ question: String, _ index: Int) -> String { "option:\(question):\(index)" }

    private func pick(_ i: Int) { store.answer(question.id, choice: .option(i)) }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(question.id).font(.prismID).foregroundStyle(Theme.question)
            Text(Theme.questionMarkdown(question.question)).font(.prismQuestion).lineLimit(8).help(question.question)
            Spacer(minLength: Space.s)
            if let (n, of) = position, of > 1 {
                Text("\(n) of \(of)").font(.prismMeta).foregroundStyle(.secondary)
            }
            Button { model.dismissQuestion() } label: { Image(systemName: "xmark").iconTarget() }
                .buttonStyle(.borderless)
                .help("Hide this question (esc)")
        }
    }

    private func optionLabel(_ i: Int, _ option: Option, mark: String, lit: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Image(systemName: mark).foregroundStyle(lit ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
            VStack(alignment: .leading, spacing: Space.xxs) {
                Text(option.label)
                if let d = option.description, !d.isEmpty {
                    Text(Theme.markdown(d)).font(.callout).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: Space.s)
            if i == question.recommendedIndex { Text("\u{2605} Recommended").font(.prismMeta).foregroundStyle(.secondary) }
            KeyHint(text: "\(i + 1)")
        }
        .padding(Space.s)
    }

    private var chip: some View {
        Button { model.dismissQuestion() } label: {
            HStack(spacing: Space.xs) {
                Text("Answering \(question.id)").font(.prismMeta.weight(.semibold))
                Image(systemName: "xmark").font(.prismMeta.weight(.bold))
            }
            .foregroundStyle(Theme.question)
            .padding(.horizontal, Space.s)
            .frame(height: Theme.chipHeight)
        }
        .buttonStyle(RowButtonStyle(fill: Theme.question.opacity(0.14)))
    }

    private var field: some View {
        @Bindable var model = model
        return TextField("or in your own words\u{2026}", text: $model.answerDraft, axis: .vertical)
            .textFieldStyle(.plain)
            .lineLimit(1...4)
            .focused(focus, equals: .answer)
            .newlineOnShiftReturn(in: $model.answerDraft)
            .onSubmit(answerInWords)
    }

    private var sendButton: some View {
        let typed = !model.answerDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        let label = typed ? "Send answer" : (question.recommendedIndex.flatMap { question.options.indices.contains($0) ? "Send \u{201C}\(question.options[$0].label)\u{201D}" : nil } ?? "Send answer")
        return Button(action: answerInWords) {
            Text(label + " \u{21A9}").font(.callout.weight(.semibold)).lineLimit(1)
        }
        .disabled(!typed && question.recommendedChoice == nil)
    }

    private var undoButton: some View {
        Button { store.undo(question.id) } label: {
            HStack(spacing: Space.s) { Text("Undo"); KeyHint(text: "z") }
        }
    }

    private func answerInWords() {
        let text = model.answerDraft.trimmingCharacters(in: .whitespacesAndNewlines)
        if text.isEmpty {
            store.answerRecommended(question.id)
            return
        }
        model.answerDraft = ""
        store.answer(question.id, choice: .text(text))
    }
}

/// Fluid's option row: the shade of a row, a small give under the press, and a background the countdown row takes over.
private struct GiveButtonStyle: ButtonStyle {
    var fill: Color
    var id: String
    var ns: Namespace.ID

    func makeBody(configuration: Configuration) -> some View {
        Target(configuration: configuration, style: self)
    }

    private struct Target: View {
        let configuration: Configuration
        let style: GiveButtonStyle
        @Environment(\.targetID) private var targetID
        @Environment(WindowModel.self) private var model: WindowModel?
        @Environment(\.accessibilityReduceMotion) private var reduce
        @State private var pointerOver = false

        var body: some View {
            let pressed = configuration.isPressed || (targetID != nil && model?.pressedTarget == targetID)
            let shade = pressed ? 0.09 : (pointerOver ? 0.05 : 0)
            let shape = RoundedRectangle(cornerRadius: Space.s, style: .continuous)
            configuration.label
                .background(shape.fill(Color.primary.opacity(shade)))
                .background(shape.fill(style.fill).matchedGeometryEffect(id: style.id, in: style.ns))
                .contentShape(shape)
                .scaleEffect(pressed && !reduce ? 0.985 : 1)
                .onHover { pointerOver = $0 }
                .animation(.spring(response: 0.22, dampingFraction: 0.7), value: pressed)
        }
    }
}

/// The pour (feel ⌥5): the field is one piece of glass that stays, and a decision is a second piece that pours out of
/// it when one loads, into a pill while its answer counts down, and back into the field when it is sent. Two glass
/// views swapped under one glassEffectID cost 0 to 2 late frames a step once their text is cached (they cost 3 to 5 before).
struct PourComposer: View {
    var store: DeskStore
    var model: WindowModel
    /// The decision in the composer, when it is one the pour draws (FeelComposer.fits).
    var question: Question?
    var position: (Int, Int)?
    var waiting: Int
    var focus: FocusState<ComposerField?>.Binding
    @Namespace private var ns
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        let parts = question.map { FeelComposer(store: store, model: model, question: $0, position: position, feel: .morph, focus: focus) }
        let pending = question.flatMap { store.answers.pending(for: $0.id) }
        let shape = RoundedRectangle(cornerRadius: Theme.composerRadius, style: .continuous)
        // The container's spacing is under the gap between the pieces, so they sit apart at rest and only run together
        // while one is changing shape.
        GlassEffectContainer(spacing: Space.xs) {
            VStack(alignment: .leading, spacing: Space.s) {
                if let parts {
                    if let pending {
                        parts.countdownContent(pending)
                            .glassEffect(.regular.interactive(), in: Capsule())
                            .glassEffectID("decision", in: ns)
                    } else {
                        parts.decisionContent
                            .glassEffect(.regular, in: shape)
                            .glassEffectID("decision", in: ns)
                    }
                }
                Group {
                    if let parts {
                        parts.answerRow(counting: pending != nil)
                    } else {
                        MessageComposer(store: store, model: model, waiting: waiting, focus: focus, bare: true)
                    }
                }
                .glassEffect(.regular.interactive(), in: shape)
                .glassEffectID("field", in: ns)
            }
        }
        .environment(\.keysLive, focus.wrappedValue == nil)
        .animation(reduce ? nil : Feel.morph.answerMotion, value: pending?.deadline)
        .animation(reduce ? nil : Feel.morph.answerMotion, value: question?.id)
    }
}

/// The pour on a board card: each option is a glass capsule, and the one you pick becomes the countdown pill while the
/// others run into it.
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
                        let recommended = i == question.recommendedIndex
                        Button { store.answer(question.id, choice: .option(i)) } label: {
                            // Ink-tinted glass read as grey with pale text in light, so the recommended one is marked
                            // by its star and weight and the glass stays clear.
                            Text(recommended ? "\u{2605} \(option.label)" : option.label)
                                .font(.callout.weight(recommended ? .semibold : .regular))
                                .lineLimit(1)
                                .padding(.horizontal, Space.m)
                                .frame(height: Space.xxl)
                                .contentShape(Capsule())
                        }
                        .buttonStyle(.plain)
                        .help(option.description ?? "")
                        .glassEffect(.regular.interactive(), in: Capsule())
                        .glassEffectID("option\(i)", in: ns)
                    }
                }
            }
        }
        .animation(reduce ? nil : Feel.morph.answerMotion, value: pending?.deadline)
    }
}

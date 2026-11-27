import DeskCore
import SwiftUI

/// Under the desk's chat: the field that messages the desk, always, and above it the question that needs Emil as a card.
struct ComposerView: View {
    var store: DeskStore
    var model: WindowModel
    @FocusState private var focus: ComposerField?
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        let s = store.snapshot
        let open = Board(s).needsYou
        let active = model.activeQuestion(in: s)
        let key = active.map { store.dialog(of: $0).count > 1 ? $0.dialog : $0.id }
        let counting = active.map { store.answers.pending(for: $0.id) != nil || store.answers.batch(containing: $0.id) != nil } ?? false
        VStack(spacing: Space.s) {
            if model.feelPicker { FeelPicker(model: model) }
            // The pour keeps one view across decisions, so its glass changes shape where the others swap.
            let pours = model.feel == .morph && (active == nil || (store.dialog(of: active!).count == 1 && FeelComposer.fits(active!, in: store)) || store.answers.pending(for: active!.id) != nil)
            if pours {
                PourComposer(store: store, model: model, question: active, position: active.flatMap { q in open.firstIndex { $0.id == q.id }.map { ($0 + 1, open.count) } }, waiting: active == nil ? open.count : 0, focus: $focus)
            } else if let q = active {
                let dialog = store.dialog(of: q)
                Group {
                    if dialog.count > 1 {
                        DecisionDialog(store: store, model: model, questions: dialog, keyed: store.nextUndecided(from: q).id, focus: $focus)
                    } else if model.feel != .today, model.feel != .morph, FeelComposer.fits(q, in: store) {
                        FeelComposer(store: store, model: model, question: q, position: open.firstIndex { $0.id == q.id }.map { ($0 + 1, open.count) }, feel: model.feel, focus: $focus)
                    } else {
                        QuestionComposer(store: store, model: model, question: q, position: open.firstIndex { $0.id == q.id }.map { ($0 + 1, open.count) }, focus: $focus)
                    }
                }
                .id(key)
                // An answer that went out leaves at once, with its ring; the next decision fades in as the height settles.
                .transition(.asymmetric(insertion: .opacity.animation(Motion.fades(Motion.settle, reduce: reduce)), removal: counting ? .identity : .opacity.animation(Motion.fades(Motion.settle, reduce: reduce))))
            }
            // One field: a single decision carries its own answer field in its box, so the message field waits behind
            // esc (the question stays open on the board). A track's dialog keeps both.
            if !pours, active == nil || store.dialog(of: active!).count > 1 {
                MessageComposer(store: store, model: model, waiting: active == nil ? open.count : 0, focus: $focus)
            }
        }
        .animation(Motion.moves(model.feel.answerMotion, reduce: reduce), value: counting)
        .animation(Motion.moves(Motion.settle, reduce: reduce), value: key)
        .padding(.horizontal, Space.m)
        .padding(.bottom, Space.l)
        // The messages' text sits Space.xxl in, so the composer's, Space.l inside its box at Space.m, starts where theirs does.
        .frame(maxWidth: Theme.chatMaxWidth)
        .frame(maxWidth: .infinity)
        .onChange(of: model.focus) { _, request in
            if let request { focus = request.field }
        }
        .task(id: active?.id) {
            if let field = ComposerFocus.initial(for: active) {
                focus = field
            } else if active != nil, model.draft.isEmpty {
                // A decision with options takes the keys: the cursor leaves the desk field so 1–9 and return reach it.
                focus = nil
                NSApp.keyWindow?.makeFirstResponder(nil)
            }
        }
    }
}

extension View {
    /// A click anywhere in the padded shape puts the caret in its text field; buttons inside keep their own clicks.
    func focusOnTap(_ focus: FocusState<ComposerField?>.Binding, _ field: ComposerField) -> some View {
        contentShape(Rectangle()).onTapGesture { focus.wrappedValue = field }
    }

    /// Shift+return makes a new line in a field that otherwise sends on return.
    func newlineOnShiftReturn(in text: Binding<String>) -> some View {
        onKeyPress(.return, phases: .down) { press in
            guard press.modifiers.contains(.shift) else { return .ignored }
            text.wrappedValue += "\n"
            return .handled
        }
    }
}

/// A dropped file above the desk field: a thumbnail for a picture, its name for anything else, and a way to take it back.
struct DraftAttachment: View {
    var url: URL
    var remove: () -> Void
    @State private var image: NSImage?

    var body: some View {
        HStack(spacing: Space.s) {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(height: Theme.composerThumbnail)
                    .clipShape(RoundedRectangle(cornerRadius: Space.s, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Space.s, style: .continuous).strokeBorder(Theme.hairline))
            } else {
                Image(systemName: "doc")
                Text(url.lastPathComponent).font(.prismMeta).lineLimit(1).truncationMode(.middle)
            }
            Button(action: remove) { Image(systemName: "xmark.circle.fill").iconTarget() }
                .buttonStyle(.borderless)
                .foregroundStyle(.secondary)
                .help("Remove \(url.lastPathComponent)")
        }
        .help(url.lastPathComponent)
        .task(id: url) {
            let url = url
            image = await Task.detached { NSImage(contentsOf: url) }.value
        }
    }
}

struct MessageComposer: View {
    var store: DeskStore
    var model: WindowModel
    var waiting: Int
    var focus: FocusState<ComposerField?>.Binding
    /// Inside the pour, which draws the glass itself.
    var bare = false

    var body: some View {
        @Bindable var model = model
        VStack(alignment: .leading, spacing: Space.s) {
            if waiting > 0 {
                Button("\(waiting) question\(waiting == 1 ? "" : "s") need\(waiting == 1 ? "s" : "") you") {
                    model.questionDismissed = false
                }
                .buttonStyle(.link)
                .font(.callout)
            }
            if !model.attachments.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: Space.m) {
                        ForEach(model.attachments, id: \.self) { url in
                            DraftAttachment(url: url) { model.attachments.removeAll { $0 == url } }
                        }
                    }
                }
            }
            HStack(spacing: Space.s) {
                TextField("Message Prism\u{2026}", text: $model.draft, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...6)
                    .focused(focus, equals: .desk)
                    .newlineOnShiftReturn(in: $model.draft)
                    .onSubmit(send)
                KeyHint(text: "\u{2318}L")
                Button("Send", action: send)
                    .buttonStyle(.borderedProminent)
                    .disabled(model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && model.attachments.isEmpty)
                    .keyboardShortcut(.return, modifiers: .command)
            }
        }
        .padding(Space.l)
        .focusOnTap(focus, .desk)
        .modifier(ComposerCard(enabled: !bare))
    }

    private func send() {
        let text = ([model.draft] + model.attachments.map(\.path)).filter { !$0.isEmpty }.joined(separator: "\n")
        model.draft = ""
        model.attachments = []
        Task { await store.sendMessage(text) }
    }
}

/// The question on its card: its numbered options with the recommendation marked, a field for an answer in words, and
/// asking the agent about it. It sits above the desk field and never replaces it.
/// A track's open decisions in one card, in merge, install, close order, since one answer can bear on another: each
/// shows its options, the keys answer the first one still undecided, and the answers go together.
struct DecisionDialog: View {
    var store: DeskStore
    var model: WindowModel
    var questions: [Question]
    /// The question the keys and the answer field act on.
    var keyed: String
    var focus: FocusState<ComposerField?>.Binding

    var body: some View {
        let s = store.snapshot
        let track = questions.first?.track.flatMap { s.track($0)?.name }
        let batch = questions.lazy.compactMap { store.answers.batch(containing: $0.id) }.first
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(spacing: Space.s) {
                Image(systemName: "square.stack.3d.up").foregroundStyle(.tint)
                Text(track ?? "Decisions").font(.callout.weight(.semibold)).lineLimit(1)
                Text("\(questions.count) decisions, answered together").font(.prismMeta).foregroundStyle(.secondary)
                Spacer(minLength: Space.s)
                Button { model.dismissQuestion() } label: { Image(systemName: "xmark").iconTarget() }
                    .buttonStyle(.borderless)
                    .help("Hide these decisions (esc)")
            }
            .padding(.horizontal, Space.s)
            if let batch {
                BatchCountdownRow(store: store, batch: batch)
            } else {
                // Short dialogs show whole; a long one scrolls rather than push the chat off the window.
                ViewThatFits(in: .vertical) {
                    list
                    ScrollViewReader { proxy in
                        ScrollView { list }
                            .scrollBounceBehavior(.basedOnSize)
                            .onAppear { proxy.scrollTo(keyed, anchor: .top) }
                            .onChange(of: keyed) { _, id in withAnimation { proxy.scrollTo(id, anchor: .top) } }
                    }
                }
                .frame(maxHeight: Theme.dialogMaxHeight)
                StagedBar(store: store, dialog: questions[0].dialog).padding(.horizontal, Space.s)
            }
        }
        .padding(.horizontal, Space.s)
        .padding(.vertical, Space.m)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: Theme.composerRadius, style: .continuous))
    }

    private var list: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            ForEach(Array(questions.enumerated()), id: \.element.id) { i, q in
                if i > 0 { Divider().padding(.horizontal, Space.s) }
                QuestionComposer(store: store, model: model, question: q, position: nil, focus: focus, embedded: true, keyed: q.id == keyed)
                    .id(q.id)
            }
        }
    }
}

struct QuestionComposer: View {
    var store: DeskStore
    var model: WindowModel
    var question: Question
    /// "1 of 2" among the questions that need Emil.
    var position: (Int, Int)?
    var focus: FocusState<ComposerField?>.Binding
    /// One of a track's decisions in a DecisionDialog, which draws the card, the countdown and the send bar once.
    var embedded = false
    /// The keys and the answer field act on this question; in a dialog only one of them has the field.
    var keyed = true

    var body: some View {
        @Bindable var model = model
        let staging = store.stagesAnswer(to: question)
        let stagedChoice = store.stagedChoice(for: question.id)
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Text(question.id)
                    .font(.prismID)
                    .foregroundStyle(keyed ? AnyShapeStyle(Theme.question) : AnyShapeStyle(.secondary))
                // Up to 8 lines shows a whole permission prompt; forcing the full height made a ~500-character
                // prompt blank the whole window. The tooltip holds anything longer.
                Text(Theme.questionMarkdown(question.question)).font(.prismQuestion).lineLimit(8)
                    .help(question.question)
                Spacer(minLength: Space.s)
                if !embedded {
                    if let (n, of) = position, of > 1 {
                        Text("\(n) of \(of)").font(.prismMeta).foregroundStyle(.secondary)
                    }
                    Button { model.dismissQuestion() } label: { Image(systemName: "xmark").iconTarget() }
                        .buttonStyle(.borderless)
                        .help("Hide this question (esc)")
                }
            }
            .padding(.horizontal, Space.s)
            if let pending = store.answers.pending(for: question.id) {
                CountdownRow(store: store, pending: pending).transition(Motion.fadeTransition)
            } else if let batch = store.answers.batch(containing: question.id) {
                if !embedded { BatchCountdownRow(store: store, batch: batch).transition(Motion.fadeTransition) }
            } else {
                Group {
                AttachmentStrip(question: question, thumbnailHeight: Theme.composerThumbnail)
                    .padding(.horizontal, Space.s)
                // The source line: where the question points, and the plain-text ways to ask for more.
                HStack(spacing: Space.m) {
                    LinkRow(links: question.openableLinks)
                    if !embedded, keyed, store.canAsk(question) { askRow }
                }
                .padding(.horizontal, Space.s)
                if let choice = stagedChoice {
                    StagedRow(label: question.label(for: choice)) { store.unstage(question.id) }
                }
                if question.isVisualChoice {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: Theme.visualCardWidth), spacing: Space.s, alignment: .top)], alignment: .leading, spacing: Space.s) {
                        ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                            VisualOptionCard(question: question, index: i, option: option, recommended: i == question.recommendedIndex, staged: stagedChoice == .option(i), otherStaged: stagedChoice != nil && stagedChoice != .option(i)) {
                                store.answer(question.id, choice: .option(i))
                            }
                        }
                    }
                } else {
                    VStack(spacing: Space.xxs) {
                        ForEach(Array(question.options.enumerated()), id: \.offset) { i, option in
                            OptionRow(question: question, index: i, option: option, recommended: i == question.recommendedIndex, staged: stagedChoice == .option(i), otherStaged: stagedChoice != nil && stagedChoice != .option(i), showsKey: keyed) {
                                store.answer(question.id, choice: .option(i))
                            }
                        }
                    }
                }
                if question.options.isEmpty, let text = question.recommendationText {
                    RecommendationRow(text: text) { store.answerRecommended(question.id) }
                }
                if keyed, !embedded {
                    answerRow(staging: staging, stagedChoice: stagedChoice)
                } else if keyed {
                    HStack(spacing: Space.s) {
                        TextField(question.options.isEmpty && question.recommendationText == nil ? "Answer in your own words\u{2026}" : "Or answer in your own words\u{2026}", text: $model.answerDraft, axis: .vertical)
                            .textFieldStyle(.plain)
                            .lineLimit(1...4)
                            .focused(focus, equals: .answer)
                            .newlineOnShiftReturn(in: $model.answerDraft)
                            .onSubmit(answerInWords)
                        Button(staging ? "Stage" : "Answer", action: answerInWords)
                            .disabled(model.answerDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                    .padding(.horizontal, Space.s)
                    .padding(.top, Space.xs)
                    .focusOnTap(focus, .answer)
                    if store.canAsk(question) { askRow }
                }
                if model.askingAbout == question.id { askField }
                if !embedded { StagedBar(store: store, dialog: question.dialog).padding(.horizontal, Space.s) }
                }
                .transition(Motion.fadeTransition)
            }
        }
        .padding(.horizontal, embedded ? 0 : Space.s)
        .padding(.vertical, embedded ? 0 : Space.m)
        .environment(\.keysLive, focus.wrappedValue == nil)
        .modifier(ComposerCard(enabled: !embedded))
    }

    /// What one send does: the typed answer, else the staged or recommended option, named so the button says what it sends.
    private func sendLabel(stagedChoice: AnswerChoice?) -> String {
        if !model.answerDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return "Send answer" }
        if let c = stagedChoice { return "Send \u{201C}\(question.label(for: c))\u{201D}" }
        if let i = question.recommendedIndex, question.options.indices.contains(i) { return "Send \u{201C}\(question.options[i].label)\u{201D}" }
        if question.recommendationText != nil { return "Send recommendation" }
        return "Send answer"
    }

    /// The bottom row of the one composer box: a chip saying which decision the field answers, the field, and an ink button.
    private func answerRow(staging: Bool, stagedChoice: AnswerChoice?) -> some View {
        @Bindable var model = model
        let empty = model.answerDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        let canSend = !empty || question.recommendedChoice != nil || stagedChoice != nil
        return VStack(spacing: Space.s) {
            Divider()
            HStack(spacing: Space.s) {
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
                .help("Back to messaging Prism (esc). The question stays open on the board.")
                TextField(question.options.isEmpty && question.recommendationText == nil ? "Answer in your own words\u{2026}" : "or in your own words\u{2026}", text: $model.answerDraft, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...4)
                    .focused(focus, equals: .answer)
                    .newlineOnShiftReturn(in: $model.answerDraft)
                    .onSubmit(answerInWords)
                Button(action: answerInWords) {
                    Text(staging && !empty ? "Stage \u{21A9}" : sendLabel(stagedChoice: stagedChoice) + " \u{21A9}")
                        .font(.callout.weight(.semibold))
                        .lineLimit(1)
                }
                .buttonStyle(InkButtonStyle())
                .disabled(!canSend)
            }
            .padding(.horizontal, Space.s)
        }
        .focusOnTap(focus, .answer)
    }

    private var askRow: some View {
        HStack(spacing: Space.m) {
            Button {
                model.askingAbout = question.id
                model.requestFocus(.ask)
            } label: {
                HStack(spacing: Space.xs) { Text("Ask for more\u{2026}"); KeyHint(text: "m") }
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .font(.callout)
            if question.canShowOptions {
                let asked = store.optionsRequested.contains(question.id)
                Button(asked ? "Asked for pictures\u{2026}" : "Show me the options") { Task { await store.showOptions(question.id) } }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
                    .font(.callout)
                    .disabled(asked)
                    .help(asked ? "Waiting for the agent to ask again with a picture per option, if its options are visual" : "Asks the agent to render each visual option to an image and ask again")
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, Space.s)
    }

    private var askField: some View {
        @Bindable var model = model
        return HStack(spacing: Space.s) {
            TextField("Ask the agent about this\u{2026}", text: $model.askDraft)
                .textFieldStyle(.roundedBorder)
                .focused(focus, equals: .ask)
                .onSubmit(sendAsk)
            Button("Ask", action: sendAsk)
                .disabled(model.askDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            Button("Cancel") { model.askingAbout = nil }
        }
        .padding(.horizontal, Space.s)
    }

    private func sendAsk() {
        let text = model.askDraft
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        model.askDraft = ""
        model.askingAbout = nil
        let id = question.id
        Task { await store.ask(id, text) }
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

/// The question's links, each a button that opens it in the browser: an MCP approval's page, opened before Done, continue.
struct LinkRow: View {
    var links: [URL]

    var body: some View {
        if !links.isEmpty {
            HStack(spacing: Space.s) {
                ForEach(links, id: \.self) { url in
                    Button { NSWorkspace.shared.open(url) } label: {
                        Label("Open \(url.host() ?? url.absoluteString)", systemImage: "arrow.up.right.square")
                    }
                    .controlSize(.small)
                    .help(url.absoluteString)
                }
                Spacer(minLength: 0)
            }
        }
    }
}

/// "Staged: Merge now", with a way back to undecided.
struct StagedRow: View {
    var label: String
    var change: () -> Void

    var body: some View {
        HStack(spacing: Space.s) {
            Image(systemName: "tray.and.arrow.up").foregroundStyle(.tint)
            Text("Staged: \(label)").fontWeight(.medium).lineLimit(2)
            Spacer(minLength: Space.s)
            Button("Change", action: change).controlSize(.small)
        }
        .padding(Space.s)
        .background(RoundedRectangle(cornerRadius: Space.s, style: .continuous).fill(Color.accentColor.opacity(0.10)))
    }
}

/// The composer's glass card, which a question inside a DecisionDialog leaves to the dialog.
struct ComposerCard: ViewModifier {
    var enabled: Bool

    func body(content: Content) -> some View {
        if enabled {
            content.glassEffect(.regular, in: RoundedRectangle(cornerRadius: Theme.composerRadius, style: .continuous))
        } else {
            content
        }
    }
}

/// "Send 2 answers", for a dialog with staged answers whose other questions are still open.
struct StagedBar: View {
    var store: DeskStore
    var dialog: String

    var body: some View {
        let count = store.stagedCount(for: dialog)
        if count > 0 {
            HStack(spacing: Space.s) {
                Image(systemName: "tray.and.arrow.up").foregroundStyle(.secondary)
                Text("\(count) staged, \(store.snapshot.openQuestions.filter { $0.dialog == dialog }.count - count) not answered yet")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                Spacer(minLength: Space.s)
                Button { store.sendStaged(dialog: dialog) } label: {
                    HStack(spacing: Space.s) { Text("Send \(count) answer\(count == 1 ? "" : "s")"); KeyHint(text: "\u{2318}\u{21A9}") }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }
        }
    }
}

/// The recommendation in words, for a question with no options: enter or a click takes it.
struct RecommendationRow: View {
    var text: String
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Image(systemName: "largecircle.fill.circle").foregroundStyle(.tint)
                Text(Theme.markdown("Recommended: \(text)")).fontWeight(.medium).multilineTextAlignment(.leading)
                Spacer(minLength: Space.s)
                KeyHint(text: "\u{21A9}")
            }
            .padding(.horizontal, Space.s)
            .padding(.vertical, Space.s)
        }
        .buttonStyle(RowButtonStyle(fill: Color.accentColor.opacity(0.10)))
    }
}

/// One option of a visual choice: its picture (click for Quick Look) above a row that picks it.
struct VisualOptionCard: View {
    var question: Question
    var index: Int
    var option: Option
    var recommended: Bool
    var staged = false
    /// Another option is staged, so this one no longer looks picked even when it is the recommended one: one mark at a
    /// time, and the Recommended pill alone says which the agent would pick (EMI-409).
    var otherStaged = false
    var action: () -> Void

    var body: some View {
        VStack(spacing: Space.xs) {
            if let a = option.attachment {
                AttachmentThumbnail(attachment: a, height: Theme.visualThumbnail) { Attachments.open(a, among: question) }
                    .frame(maxWidth: .infinity)
                    .frame(height: Theme.visualThumbnail)
            }
            Button(action: action) {
                HStack(spacing: Space.s) {
                    Image(systemName: staged ? "checkmark.circle.fill" : (recommended && !otherStaged ? "largecircle.fill.circle" : "circle"))
                        .foregroundStyle(staged || (recommended && !otherStaged) ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                    Text(option.label).lineLimit(2).multilineTextAlignment(.leading)
                    Spacer(minLength: 0)
                    KeyHint(text: "\(index + 1)")
                }
                .padding(.horizontal, Space.s)
                .padding(.vertical, Space.s)
            }
            .buttonStyle(RowButtonStyle())
        }
        .padding(Space.s)
        .background(
            RoundedRectangle(cornerRadius: Space.s, style: .continuous)
                .fill(staged || (recommended && !otherStaged) ? Color.accentColor.opacity(staged ? 0.18 : 0.10) : Color.primary.opacity(0.03))
        )
    }
}

struct OptionRow: View {
    var question: Question
    var index: Int
    var option: Option
    var recommended: Bool
    var staged = false
    /// Another option is staged, so this one no longer looks picked even when it is the recommended one: one mark at a
    /// time, and the Recommended pill alone says which the agent would pick (EMI-409).
    var otherStaged = false
    /// The digit keys pick from this question; in a dialog only the keyed one shows them.
    var showsKey = true
    var action: () -> Void

    var body: some View {
        HStack(spacing: Space.s) {
            if let a = option.attachment, a.attachmentKind == .image {
                AttachmentThumbnail(attachment: a, height: Theme.rowThumbnail) { Attachments.open(a, among: question) }
                    .frame(width: Theme.rowThumbnail)
                    .padding(.leading, Space.s)
            }
            row
            if let a = option.attachment, a.attachmentKind != .image {
                AttachmentButton(attachment: a) { Attachments.open(a, among: question) }
                    .padding(.trailing, Space.s)
            }
        }
        .background(
            RoundedRectangle(cornerRadius: Space.s, style: .continuous)
                .fill(staged || (recommended && !otherStaged) ? Color.accentColor.opacity(staged ? 0.18 : 0.10) : .clear)
        )
    }

    private var row: some View {
        Button(action: action) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Image(systemName: staged ? "checkmark.circle.fill" : (recommended && !otherStaged ? "largecircle.fill.circle" : "circle"))
                    .foregroundStyle(staged || (recommended && !otherStaged) ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                VStack(alignment: .leading, spacing: Space.xxs) {
                    Text(option.label)
                    if let d = option.description, !d.isEmpty {
                        Text(Theme.markdown(d)).font(.callout).foregroundStyle(.secondary)
                    }
                }
                Spacer(minLength: Space.s)
                if recommended { Text("\u{2605} Recommended").font(.prismMeta).foregroundStyle(.secondary) }
                if showsKey { KeyHint(text: "\(index + 1)") }
            }
            .padding(.horizontal, Space.s)
            .padding(.vertical, Space.s)
        }
        .buttonStyle(RowButtonStyle())
    }
}

/// The ring that drains and the seconds left in an undo window: the ring runs down in one linear animation, the digit
/// swaps once a second on the deadline's own beat.
struct CountdownLine: View {
    var text: (Int) -> String
    @State private var end: Date
    @State private var level: Double

    init(deadline: Date, now: Date, text: @escaping (Int) -> String) {
        let left = max(0, deadline.timeIntervalSince(now))
        self.text = text
        _end = State(initialValue: Date().addingTimeInterval(left))
        _level = State(initialValue: left / AnswerQueue.undoWindow)
    }

    var body: some View {
        HStack(spacing: Space.s) {
            Circle()
                .stroke(Color.primary.opacity(0.12), lineWidth: Theme.ringWidth)
                .overlay {
                    Circle()
                        .trim(from: 0, to: level)
                        .stroke(.tint, style: StrokeStyle(lineWidth: Theme.ringWidth, lineCap: .round))
                        .rotationEffect(.degrees(-90))
                }
                .frame(width: Theme.ringSize, height: Theme.ringSize)
                .onAppear { withAnimation(Motion.drain(max(0, end.timeIntervalSinceNow))) { level = 0 } }
            TimelineView(.explicit(ticks)) { context in
                Text(text(max(0, Int(end.timeIntervalSince(context.date).rounded(.up))))).monospacedDigit()
            }
        }
    }

    /// One tick just after each whole second left, so the digit changes when it should and not a frame early.
    private var ticks: [Date] {
        (1...Int(AnswerQueue.undoWindow)).map { end.addingTimeInterval(0.05 - Double($0)) }.sorted()
    }
}

/// "Sending “Merge now” in 4 s", with Undo, while the 5-second window runs. On the board the option's name is left out.
struct CountdownRow: View {
    var store: DeskStore
    var pending: PendingAnswer
    var named = true

    var body: some View {
        HStack(spacing: Space.s) {
            CountdownLine(deadline: pending.deadline, now: store.now) { left in
                named ? "Sending \u{201C}\(pending.label)\u{201D} in \(left) s" : "Sending in \(left) s"
            }
            if pending.againstRecommendation { Pill(text: "Not your recommendation", color: .orange) }
            Spacer(minLength: Space.s)
            Button { store.undo(pending.question) } label: {
                HStack(spacing: Space.s) { Text("Undo"); KeyHint(text: "z") }
            }
        }
        .padding(Space.s)
        .id(pending.deadline)
    }
}

/// "Sending 3 answers in 4 s", with Undo, while the one 5-second window of a batch runs.
struct BatchCountdownRow: View {
    var store: DeskStore
    var batch: PendingBatch

    var body: some View {
        HStack(spacing: Space.s) {
            CountdownLine(deadline: batch.deadline, now: store.now) { left in
                "Sending \(batch.count) answer\(batch.count == 1 ? "" : "s") in \(left) s"
            }
            if batch.againstCount > 0 { Pill(text: "\(batch.againstCount) not your recommendation", color: .orange) }
            Spacer(minLength: Space.s)
            Button { store.undo(batch.items.first?.question) } label: {
                HStack(spacing: Space.s) { Text("Undo"); KeyHint(text: "z") }
            }
        }
        .padding(Space.s)
        .id(batch.deadline)
    }
}

import AppKit
import DeskCore
import SwiftUI

/// The board's way into the sheet: a small borderless moon.
struct OvernightButton: View {
    var model: WindowModel

    var body: some View {
        Button { model.overnightShown = true } label: {
            Image(systemName: "moon.stars").iconTarget()
        }
        .buttonStyle(.borderless)
        .controlSize(.small)
        .help("Run overnight\u{2026}")
    }
}

/// "Run overnight": the goal, folder and limits of a loop, sent to the desk as one ordinary message.
struct OvernightSheet: View {
    var store: DeskStore
    var model: WindowModel
    @State private var loop: OvernightLoop
    @State private var sending = false
    @State private var failure: String?

    init(store: DeskStore, model: WindowModel) {
        self.store = store
        self.model = model
        _loop = State(initialValue: model.overnightSeed ?? OvernightLoop(
            folder: OvernightLoop.folder(of: model.session(in: store.snapshot).flatMap { $0.track.flatMap(store.snapshot.track) }) ?? "",
            until: .standard(at: store.now)
        ))
    }

    var body: some View {
        VStack(spacing: Space.none) {
            Form {
                Section("What should it keep improving?") {
                    TextField("What should it keep improving?", text: $loop.goal, prompt: Text("Required"), axis: .vertical)
                        .labelsHidden()
                        .lineLimit(3...6)
                    LabeledContent("Folder") {
                        HStack(spacing: Space.s) {
                            TextField("Folder", text: $loop.folder, prompt: Text("Required"))
                                .labelsHidden()
                                .multilineTextAlignment(.leading)
                                .font(.body.monospaced())
                            Button("Choose\u{2026}", action: choose)
                        }
                    }
                }
                Section {
                    Picker("Until", selection: $loop.until) {
                        ForEach(OvernightLoop.Until.allCases) { Text($0.label).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    Picker("At most", selection: $loop.turns) {
                        ForEach(OvernightLoop.turnChoices, id: \.self) { Text("\($0) turns").tag($0) }
                    }
                    .pickerStyle(.segmented)
                    Picker("Usage budget", selection: $loop.budget) {
                        ForEach(OvernightLoop.budgetChoices, id: \.self) { Text("\($0) %").tag($0) }
                    }
                    .pickerStyle(.segmented)
                } footer: {
                    Text("Stops at commits on its own branch. Never merges, pushes or asks; permission prompts are denied after 2 minutes.")
                        .padding(.top, Space.xs)
                }
            }
            .formStyle(.grouped)
            HStack(spacing: Space.s) {
                if let failure {
                    Text(failure).font(.callout).foregroundStyle(.secondary).lineLimit(2)
                }
                Spacer(minLength: Space.s)
                Button("Cancel") { model.overnightShown = false }
                    .keyboardShortcut(.cancelAction)
                Button("Start loop", action: start)
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(!loop.isReady || sending)
            }
            .padding(.horizontal, Space.l)
            .padding(.bottom, Space.l)
        }
        .navigationTitle("Run overnight")
        .frame(width: 480)
        .fixedSize(horizontal: false, vertical: true)
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        let done: (NSApplication.ModalResponse) -> Void = { response in
            if response == .OK, let url = panel.url { loop.folder = url.path }
        }
        if let window = NSApp.keyWindow ?? NSApp.windows.first(where: \.isVisible) {
            panel.beginSheetModal(for: window, completionHandler: done)
        } else {
            panel.begin(completionHandler: done)
        }
    }

    private func start() {
        sending = true
        failure = nil
        Task {
            let sent = await store.sendMessage(loop.message)
            sending = false
            if sent {
                model.selection = .desk
                model.overnightShown = false
            } else {
                failure = store.notice?.text ?? "Couldn't send"
                store.notice = nil
            }
        }
    }
}

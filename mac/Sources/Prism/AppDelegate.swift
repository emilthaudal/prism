import AppKit
import DeskCore
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let options: LaunchOptions
    private let store: DeskStore
    private let fixture: FixtureClient?
    private let model = WindowModel()
    private var window: NSWindow?
    private var notifier: Notifier?
    private var syncTask: Task<Void, Never>?
    private var tickTask: Task<Void, Never>?
    private var keyMonitor: Any?
    private var player: ScriptPlayer?
    private var awayObserver: AwayObserver?

    /// How often due answers are sent; the countdown itself redraws on its own.
    private static let tickInterval: Duration = .milliseconds(200)

    init(options: LaunchOptions) {
        self.options = options
        if let path = options.fixture {
            // The fixture's clock starts at its generated_at, so "2m ago" reads as it did when it was written.
            let start = Date()
            let base = (try? Data(contentsOf: path)).flatMap { try? DeskJSON.decoder().decode(Snapshot.self, from: $0) }?.generatedAt ?? start
            let clock: @Sendable () -> Date = { base.addingTimeInterval(Date().timeIntervalSince(start)) }
            do {
                let client = try FixtureClient.load(path, clock: clock)
                fixture = client
                store = DeskStore(client: client, clock: clock)
            } catch {
                FileHandle.standardError.write(Data("prism: can't read \(path.path): \(error)\n".utf8))
                exit(1)
            }
        } else {
            fixture = nil
            store = DeskStore(client: HTTPClient())
            let launcher = ServeLauncher()
            store.prepare = { try await launcher.ensureRunning() }
        }
        super.init()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        switch options.appearance {
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        default: break
        }
        installMenu()
        model.away = AwayWatch.forRun(fixture: fixture != nil, awaySince: options.awaySince)

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1320, height: 820),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = "Prism"
        window.isReleasedWhenClosed = false
        window.toolbarStyle = .unified
        // The wall runs up under the title bar, as in the mockup: no flat bar across the conversation and the board.
        window.titlebarAppearsTransparent = true
        if options.sidebarOnly && fixture != nil {
            // The sidebar alone, outside the split view's glass, so --snapshot can draw its rows.
            window.setContentSize(NSSize(width: 380, height: 820))
            window.contentView = NSHostingView(rootView: SidebarView(store: store, model: model).background(PrismWall()))
        } else {
            let host = NSHostingView(rootView: DeskWindow(store: store, model: model, fixture: fixture != nil))
            host.sceneBridgingOptions = [.toolbars, .title]
            window.contentView = host
        }
        window.center()
        if !options.background { window.setFrameAutosaveName("DeskWindow") }
        self.window = window
        awayObserver = AwayObserver(model: model, window: window, now: { [store] in store.now })
        // A recording films a window that other windows may cover, so it keeps animating.
        if options.motionDir == nil && options.uiDir == nil {
            for name in [NSWindow.didChangeOcclusionStateNotification, NSWindow.didMiniaturizeNotification, NSWindow.didDeminiaturizeNotification] {
                NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                    MainActor.assumeIsolated { self?.updateVisibility() }
                }
            }
        }

        if options.demoOvernight, fixture != nil {
            model.overnightSeed = OvernightLoop(goal: "Get p95 of /search under 120 ms on the bench set. Measure with make bench.", folder: "~/code/pantry-api", until: .sevenOClock)
            model.overnightShown = true
        }
        if let lab = options.lab, fixture != nil {
            model.lab = lab
            model.labShown = true
        }
        if options.select != nil || options.demoAnswer || options.demoStage || options.demoPick != nil { preselect(options.select) }

        if let path = options.demoScript, let fixture, let view = window.contentView {
            if options.uiDir != nil { model.pointerLive = false }
            do {
                player = ScriptPlayer(script: try DemoScript.load(path), store: store, fixture: fixture, model: model, motionDir: options.motionDir, uiDir: options.uiDir)
                player?.play(in: view)
            } catch {
                FileHandle.standardError.write(Data("prism: can't read \(path.path): \(error)\n".utf8))
                exit(1)
            }
        }

        if options.background {
            window.orderBack(nil)
        } else {
            window.makeKeyAndOrderFront(nil)
            NSApp.activate()
        }

        if !options.background, Notifier.available {
            let notifier = Notifier(store: store) { [weak self] id in self?.showQuestion(id) }
            notifier.start()
            store.onNewQuestion = { [weak self, weak notifier] q in
                guard let self else { return }
                notifier?.post(q, track: self.store.snapshot.track(q.track))
            }
            store.onQuestionClosed = { [weak notifier] id in notifier?.remove(id) }
            self.notifier = notifier
        }

        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            let handled = MainActor.assumeIsolated { self?.handle(event) == true }
            return handled ? nil : event
        }
        store.onRetry = { [weak self] in self?.restartSync() }
        restartSync()
        if let out = options.snapshot {
            Task { [weak self] in
                try? await Task.sleep(for: .seconds(3))
                self?.writeSnapshot(to: out)
                NSApp.terminate(nil)
            }
        }
        tickTask = Task { [store] in
            while !Task.isCancelled {
                try? await Task.sleep(for: Self.tickInterval)
                await store.tick()
            }
        }
    }

    /// Draws the window's frame view (title bar included) into a PNG, in-process, so no Screen Recording grant is needed.
    private func writeSnapshot(to url: URL) {
        guard let view = window?.contentView?.superview ?? window?.contentView,
              let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return }
        view.cacheDisplay(in: view.bounds, to: rep)
        do {
            try rep.representation(using: .png, properties: [:])?.write(to: url)
        } catch {
            FileHandle.standardError.write(Data("prism: can't write \(url.path): \(error)\n".utf8))
        }
    }

    private func restartSync() {
        syncTask?.cancel()
        syncTask = Task { [store] in await store.run() }
    }

    func applicationWillTerminate(_ notification: Notification) {
        syncTask?.cancel()
        tickTask?.cancel()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { showWindow() }
        return true
    }

    private func showWindow() {
        guard let window else { return }
        if window.isMiniaturized { window.deminiaturize(nil) }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    private func showQuestion(_ id: String) {
        model.selection = .desk
        model.pick(id)
        showWindow()
    }

    /// `--select` names a question (the composer answers it) or a session (the middle shows its transcript).
    private func preselect(_ id: String?) {
        let demoAnswer = options.demoAnswer && fixture != nil
        let demoStage = options.demoStage && fixture != nil
        let demoPick = fixture != nil ? options.demoPick : nil
        Task { [model, store] in
            for _ in 0..<50 where !store.loaded { try? await Task.sleep(for: .milliseconds(20)) }
            defer {
                if let n = demoPick, let q = model.activeQuestion(in: store.snapshot) { store.answer(q.id, choice: .option(n - 1)) }
                if demoAnswer, let q = model.activeQuestion(in: store.snapshot) { store.answerRecommended(q.id) }
                if demoStage, let active = model.activeQuestion(in: store.snapshot) {
                    for q in store.batchGroup(of: active) where q.id != active.id { store.answerRecommended(q.id) }
                }
            }
            guard let id else { return }
            if store.snapshot.session(id) != nil {
                model.selection = .session(id)
            } else if let s = store.snapshot.sessions.first(where: { $0.short == id || $0.name == id }) {
                model.selection = .session(s.id)
            } else {
                model.pick(id)
            }
        }
    }

    // MARK: Keys

    private func handle(_ event: NSEvent) -> Bool {
        guard event.window === window else { return false }
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        let key = KeyPress(
            characters: event.charactersIgnoringModifiers ?? "",
            control: flags.contains(.control),
            command: flags.contains(.command),
            option: flags.contains(.option),
            isReturn: event.keyCode == 36 || event.keyCode == 76,
            isEscape: event.keyCode == 53
        )
        // ⇧⌘L is the menu's Run Overnight, not ⌘L.
        if flags.contains([.command, .shift]), key.characters.lowercased() == "l" { return false }
        if model.labShown, flags == .option, let n = Int(key.characters), Lab.choices.indices.contains(n - 1) {
            model.lab.step(n - 1)
            return true
        }
        let editing = window?.firstResponder is NSTextView
        guard let command = Keys.command(for: key, editing: editing) else { return false }
        // ⌘↩ sends a message being typed to the desk; the staged answers go when there is none.
        if command == .sendStaged, editing, !model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return false }
        return perform(command)
    }

    @discardableResult
    private func perform(_ command: KeyCommand) -> Bool {
        let s = store.snapshot
        switch command {
        case .takeRecommendation:
            guard let q = model.keyedQuestion(store), q.recommendedChoice != nil, store.answers.pending(for: q.id) == nil, store.answers.batch(containing: q.id) == nil else { return false }
            store.answerRecommended(q.id)
        case .pick(let n):
            guard let q = model.keyedQuestion(store), q.options.indices.contains(n), store.answers.batch(containing: q.id) == nil else { return false }
            // The digit shows on its row for a moment, as a click would.
            model.flash(OptionRow.target(q.id, n))
            store.answer(q.id, choice: .option(n))
        case .undo:
            return store.undo()
        case .takeOver:
            guard let session = model.session(in: s), !session.isEmils else { return false }
            Task { await store.takeOver(session.id) }
        case .giveBack:
            guard let session = model.session(in: s), session.isEmils else { return false }
            Task { await store.giveBack(session.id) }
        case .preview:
            guard let q = model.keyedQuestion(store), !q.previewableImages.isEmpty else { return false }
            Attachments.preview(q)
        case .askMore:
            guard let q = model.keyedQuestion(store), store.canAsk(q) else { return false }
            model.askingAbout = q.id
            model.requestFocus(.ask)
        case .focusDesk:
            model.requestFocus(.desk)
        case .sendStaged:
            guard let dialog = store.stagedDialog(preferring: model.activeQuestion(in: s)?.id) else { return false }
            store.sendStaged(dialog: dialog)
        case .dismissQuestion:
            if model.askingAbout != nil {
                model.askingAbout = nil
                return true
            }
            guard model.activeQuestion(in: s) != nil else { return false }
            model.dismissQuestion()
        }
        return true
    }

    // MARK: Menu

    @objc private func takeRecommendation() { perform(.takeRecommendation) }
    @objc private func undoAnswer() { perform(.undo) }
    @objc private func sendStaged() { perform(.sendStaged) }
    @objc private func askMore() { perform(.askMore) }
    @objc private func focusDesk() { perform(.focusDesk) }
    @objc private func runOvernight() { model.overnightShown = true }
    @objc private func takeOver() { perform(.takeOver) }
    @objc private func giveBack() { perform(.giveBack) }
    private func updateVisibility() {
        guard let window else { return }
        model.windowVisible = window.occlusionState.contains(.visible) && !window.isMiniaturized
    }

    @objc private func toggleBoard() { model.boardShown.toggle() }
    @objc private func showDesk() { model.selection = .desk }
    @objc private func restartDesk() { Task { await store.restartDesk() } }

    @objc private func raiseTestQuestion() {
        guard let fixture else { return }
        let track = store.snapshot.tracks.first
        let question = Question(
            id: "",
            track: track?.id,
            from: "desk",
            question: "Open the queue-api PR now?",
            options: [Option(label: "Open it", description: "verify passed, +14 \u{2212}14 in 2 files"), Option(label: "Wait", description: "Until pantry-api is done too.")],
            recommended: 0,
            evidence: [Evidence(text: "verify pass", source: "Stop hook")]
        )
        Task { await fixture.raise(question) }
    }

    private func installMenu() {
        let main = NSMenu()

        let app = NSMenu()
        app.addItem(withTitle: "About Prism", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        app.addItem(.separator())
        app.addItem(withTitle: "Restart Desk", action: #selector(restartDesk), keyEquivalent: "").target = self
        app.addItem(.separator())
        app.addItem(withTitle: "Hide Prism", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        app.addItem(.separator())
        app.addItem(withTitle: "Quit Prism", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        main.addItem(submenu: app, title: "Prism")

        let edit = NSMenu(title: "Edit")
        edit.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        edit.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        edit.addItem(.separator())
        edit.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        main.addItem(submenu: edit, title: "Edit")

        let view = NSMenu(title: "View")
        view.addItem(withTitle: "Prism", action: #selector(showDesk), keyEquivalent: "0").target = self
        view.addItem(withTitle: "Show Board", action: #selector(toggleBoard), keyEquivalent: "b").target = self
        view.addItem(withTitle: "Toggle Sidebar", action: #selector(NSSplitViewController.toggleSidebar(_:)), keyEquivalent: "s").keyEquivalentModifierMask = [.command, .control]
        main.addItem(submenu: view, title: "View")

        let answer = NSMenu(title: "Answer")
        answer.addItem(withTitle: "Take Recommendation (return)", action: #selector(takeRecommendation), keyEquivalent: "").target = self
        answer.addItem(withTitle: "Undo Answer (z)", action: #selector(undoAnswer), keyEquivalent: "").target = self
        answer.addItem(withTitle: "Send Staged Answers (\u{2318}\u{21A9})", action: #selector(sendStaged), keyEquivalent: "").target = self
        answer.addItem(withTitle: "Ask for More (m)", action: #selector(askMore), keyEquivalent: "").target = self
        answer.addItem(withTitle: "Message Prism (\u{2318}L)", action: #selector(focusDesk), keyEquivalent: "").target = self
        let overnight = answer.addItem(withTitle: "Run Overnight\u{2026}", action: #selector(runOvernight), keyEquivalent: "l")
        overnight.keyEquivalentModifierMask = [.command, .shift]
        overnight.target = self
        answer.addItem(.separator())
        answer.addItem(withTitle: "Take Over Session (t)", action: #selector(takeOver), keyEquivalent: "").target = self
        answer.addItem(withTitle: "Give Back to Prism (g)", action: #selector(giveBack), keyEquivalent: "").target = self
        if fixture != nil {
            answer.addItem(.separator())
            answer.addItem(withTitle: "Raise a Test Question", action: #selector(raiseTestQuestion), keyEquivalent: "").target = self
        }
        main.addItem(submenu: answer, title: "Answer")

        let windowMenu = NSMenu(title: "Window")
        windowMenu.addItem(withTitle: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        windowMenu.addItem(withTitle: "Close", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        main.addItem(submenu: windowMenu, title: "Window")
        NSApp.windowsMenu = windowMenu
        NSApp.mainMenu = main
    }
}

private extension NSMenu {
    func addItem(submenu: NSMenu, title: String) {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.submenu = submenu
        addItem(item)
    }
}

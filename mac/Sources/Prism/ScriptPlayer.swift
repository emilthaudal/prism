import AppKit
import DeskCore
import MotionCheck
import os

/// Plays a demo script on the fixture: each step at its time, inside a signpost, while a display link notes the frame
/// times the app sees. With a motion directory it waits for the recorder first and writes the timings when done.
@MainActor
final class ScriptPlayer: NSObject {
    private let script: DemoScript
    private let store: DeskStore
    private let fixture: FixtureClient
    private let model: WindowModel
    private let motionDir: URL?
    private let uiDir: URL?
    private var ticks: [Double] = []
    private var link: CADisplayLink?
    private let signposter = OSSignposter(subsystem: "dk.thaudal.prism", category: "motion")

    /// How long to wait for the recorder (or, for stills, the driving script with the window's id) before playing anyway.
    private static let recorderWait: Duration = .seconds(15)
    /// How long a state settles (glass, easing, the caret's blink) before a still of it is asked for, and how long the
    /// still may take to arrive.
    private static let shotSettle: Duration = .milliseconds(700)
    private static let shotWait: Duration = .seconds(15)

    init(script: DemoScript, store: DeskStore, fixture: FixtureClient, model: WindowModel, motionDir: URL?, uiDir: URL? = nil) {
        self.script = script
        self.store = store
        self.fixture = fixture
        self.model = model
        self.motionDir = motionDir
        self.uiDir = uiDir
    }

    func play(in view: NSView) {
        Task {
            while !store.loaded { try? await Task.sleep(for: .milliseconds(20)) }
            if let dir = motionDir ?? uiDir { await waitForRecorder(dir) }
            link = view.displayLink(target: self, selector: #selector(frame(_:)))
            link?.add(to: .main, forMode: .common)

            let leadStart = CACurrentMediaTime()
            let start = leadStart + script.lead
            var played: [Timings.Step] = []
            var interval: OSSignpostIntervalState?
            for step in script.steps {
                await sleep(until: start + step.at)
                if let interval { signposter.endInterval("step", interval) }
                interval = signposter.beginInterval("step", id: signposter.makeSignpostID(), "\(step.name)")
                played.append(Timings.Step(name: step.name, start: CACurrentMediaTime(), expect: step.expect, continuous: step.continuous, crossfade: step.crossfade))
                await perform(step.action)
                if let name = step.shot { await shoot(name) }
                if let again = step.again {
                    Task {
                        try? await Task.sleep(for: .seconds(again))
                        await perform(step.action)
                    }
                }
            }
            await sleep(until: start + (script.steps.last?.at ?? 0) + script.tail)
            if let interval { signposter.endInterval("step", interval) }
            link?.invalidate()
            if let uiDir { FileManager.default.createFile(atPath: uiDir.appendingPathComponent("done").path, contents: nil) }
            guard let dir = motionDir else { return }
            let timings = Timings(leadStart: leadStart, end: CACurrentMediaTime(), reduceMotion: NSWorkspace.shared.accessibilityDisplayShouldReduceMotion, steps: played, ticks: ticks)
            do {
                try JSONEncoder().encode(timings).write(to: dir.appendingPathComponent(RunFiles.timings), options: .atomic)
            } catch {
                FileHandle.standardError.write(Data("prism: can't write timings: \(error)\n".utf8))
            }
        }
    }

    @objc private func frame(_ link: CADisplayLink) { ticks.append(link.timestamp) }

    private func waitForRecorder(_ dir: URL) async {
        let flag = dir.appendingPathComponent(RunFiles.recording).path
        let deadline = ContinuousClock.now + Self.recorderWait
        while !FileManager.default.fileExists(atPath: flag), ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    /// Asks the driving script for a still of the window (scripts/ui-check.sh sees the request, runs screencapture on the
    /// window's id and writes NAME.png) and waits for it, so the next step doesn't change what is being filmed.
    private func shoot(_ name: String) async {
        guard let uiDir else { return }
        try? await Task.sleep(for: Self.shotSettle)
        let png = uiDir.appendingPathComponent("\(name).png")
        try? FileManager.default.removeItem(at: png)
        FileManager.default.createFile(atPath: uiDir.appendingPathComponent("\(name).request").path, contents: nil)
        let deadline = ContinuousClock.now + Self.shotWait
        while !FileManager.default.fileExists(atPath: png.path), ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    private func sleep(until hostTime: Double) async {
        let wait = hostTime - CACurrentMediaTime()
        if wait > 0 { try? await Task.sleep(for: .seconds(wait)) }
    }

    private func perform(_ action: DemoScript.Action) async {
        switch action {
        case .event(let change): await fixture.inject(change)
        case .events(let changes): for change in changes { await fixture.inject(change) }
        case .scroll(let direction): model.scroll(direction)
        case .pick(let id): model.pick(id)
        case .answer(let id, let option):
            if let n = option { store.answer(id, choice: .option(n - 1)) } else { store.answerRecommended(id) }
        case .undo(let id): store.undo(id)
        case .mode(let track, let mode): _ = try? await fixture.perform(.mode(track: track, mode: mode))
        case .fold(let target): model.fold(target)
        case .hover(let target): model.hoveredTarget = target.isEmpty ? nil : target
        case .press(let target): model.pressedTarget = target.isEmpty ? nil : target
        case .focus(let field):
            switch field {
            case "desk": model.requestFocus(.desk)
            case "answer": model.requestFocus(.answer)
            case "ask":
                // The ask field shows once the question is asked about, as the m key does.
                model.askingAbout = model.activeQuestion(in: store.snapshot)?.id
                model.requestFocus(.ask)
            default: FileHandle.standardError.write(Data("prism: unknown field \(field)\n".utf8))
            }
        case .select(let item): model.selection = Self.selection(item)
        case .dismiss: model.dismissQuestion()
        case .draft(let text): model.draft = text
        case .jump(let entry):
            // A board row lands on the question and shades its answer; a mark lands on the message alone.
            if let row = Answers.replies(store.snapshot.conversation).first(where: { $0.id == entry }) {
                model.jump(to: row.anchor, landing: row.id)
            } else {
                model.askedCursor = entry
                model.jump(to: entry)
            }
        case .keep(let entry): model.toggleKept(entry)
        case .wait: break
        }
    }

    private static func selection(_ item: String) -> SidebarItem? {
        let parts = item.split(separator: ":", maxSplits: 1).map(String.init)
        switch (parts.first, parts.last) {
        case ("desk", _): return .desk
        case ("track", let id?) where parts.count == 2: return .track(id)
        case ("session", let id?) where parts.count == 2: return .session(id)
        default: return nil
        }
    }
}

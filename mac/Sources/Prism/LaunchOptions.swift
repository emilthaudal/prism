import Foundation

/// `Prism [--fixture PATH] [--appearance light|dark] [--background] [--select ID] [--away-since ISO8601] [--demo-answer] [--demo-stage] [--demo-pick N] [--demo-overnight] [--demo-notice] [--demo-whole] [--lab notice=banner|toast|card] [--demo-script FILE [--motion-dir DIR | --ui-dir DIR]] [--snapshot PNG]`.
struct LaunchOptions {
    static let usage = "usage: Prism [--fixture PATH] [--appearance light|dark] [--background] [--select QUESTION-OR-SESSION-ID] [--away-since ISO8601] [--demo-answer] [--demo-stage] [--demo-pick N] [--demo-overnight] [--demo-notice] [--demo-whole] [--lab notice=banner|toast|card] [--demo-script FILE [--motion-dir DIR | --ui-dir DIR]] [--snapshot PNG]"

    /// Runs on this snapshot in memory instead of prism server.
    var fixture: URL?
    var appearance: String?
    /// For captures: no Dock icon, never active or key, no notifications.
    var background = false
    var select: String?
    /// Sets when the window was last looked at for this run, so the "While you were away" digest shows; never written to the user defaults.
    var awaySince: Date?
    /// Fixture captures only: takes the recommendation once loaded, so the countdown shows without a key press.
    var demoAnswer = false
    /// Fixture captures only: stages the recommended answer to the asker's other open questions, so the batch shows.
    var demoStage = false
    /// Fixture captures only: picks option N (from 1) of the selected question, as a click on it would.
    var demoPick: Int?
    /// Fixture captures only: a window with the sidebar alone, off the glass, so --snapshot draws its rows.
    var sidebarOnly = false
    /// Fixture only: opens the "Run overnight" sheet filled with made-up values.
    var demoOvernight = false
    /// Fixture only: shows "Asked TRACK about ID" for the --select'ed question, kept until dismissed.
    var demoNotice = false
    /// Fixture captures only: a long question's body opens whole.
    var demoWhole = false
    /// Fixture only, EMI-500 prototypes: shows the notice picker, opened on this style (see NoticeStyle).
    var noticeLab: NoticeStyle?
    /// Fixture only: plays this script's timed changes (see scripts/motion-sample.json).
    var demoScript: URL?
    /// With --demo-script: waits for the recorder's flag here, then writes the step timings here (make motion).
    var motionDir: URL?
    /// With --demo-script: writes a request file here for each step's still and waits for the PNG (make ui-check).
    var uiDir: URL?
    /// Draws the window's views into this PNG once settled, then quits: a capture without Screen Recording. Window
    /// materials (glass, blur) don't draw this way, and on macOS 26 neither does the sidebar, whose glass panel draws
    /// in its own layers: it shows the conversation and the board.
    var snapshot: URL?

    struct Invalid: Error, CustomStringConvertible { var description: String }

    init(arguments: [String]) throws {
        var it = arguments.makeIterator()
        while let arg = it.next() {
            switch arg {
            case "--fixture": fixture = URL(fileURLWithPath: try Self.value(arg, &it))
            case "--appearance": appearance = try Self.value(arg, &it)
            case "--background": background = true
            case "--demo-answer": demoAnswer = true
            case "--demo-stage": demoStage = true
            case "--demo-pick":
                let v = try Self.value(arg, &it)
                guard let n = Int(v), n >= 1 else { throw Invalid(description: "--demo-pick needs an option number from 1: \(v)") }
                demoPick = n
            case "--sidebar-only": sidebarOnly = true
            case "--demo-overnight": demoOvernight = true
            case "--demo-notice": demoNotice = true
            case "--demo-whole": demoWhole = true
            case "--lab": noticeLab = try NoticeStyle(lab: try Self.value(arg, &it))
            case "--demo-script": demoScript = URL(fileURLWithPath: try Self.value(arg, &it))
            case "--motion-dir": motionDir = URL(fileURLWithPath: try Self.value(arg, &it))
            case "--ui-dir": uiDir = URL(fileURLWithPath: try Self.value(arg, &it))
            case "--away-since":
                let v = try Self.value(arg, &it)
                guard let date = try? Date.ISO8601FormatStyle().parse(v) else { throw Invalid(description: "--away-since needs an ISO 8601 time: \(v)") }
                awaySince = date
            case "--select": select = try Self.value(arg, &it)
            case "--snapshot": snapshot = URL(fileURLWithPath: try Self.value(arg, &it))
            // AppKit and Xcode pass these; they are not ours. AppKit reads -reduceMotion YES as Reduce Motion for this
            // process alone.
            case let a where a.hasPrefix("-NS") || a.hasPrefix("-Apple") || a == "-reduceMotion": _ = it.next()
            default: throw Invalid(description: "unknown argument \(arg)")
            }
        }
    }

    private static func value(_ flag: String, _ it: inout IndexingIterator<[String]>) throws -> String {
        guard let v = it.next() else { throw Invalid(description: "\(flag) needs a value") }
        return v
    }
}

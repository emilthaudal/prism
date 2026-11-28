import DeskCore
import Observation
import SwiftUI

enum SidebarItem: Hashable {
    case desk
    case track(String)
    case session(String)
}

/// What the window shows: the sidebar selection, the question the composer answers, the draft and the board.
@MainActor
@Observable
final class WindowModel {
    var selection: SidebarItem? = .desk
    /// The question picked on the board; nil follows the first one that needs Emil.
    var pickedQuestion: String?
    /// Escape puts the composer back to messaging the desk until another question is picked.
    var questionDismissed = false
    /// The message to the desk.
    var draft = ""
    /// Files dropped on the desk column, shown as thumbnails above the field and sent as paths with the message.
    var attachments: [URL] = []
    /// The answer in words to the question on the card above the desk field.
    var answerDraft = ""
    /// The question whose "Ask for more" field is open, and what is typed in it.
    var askingAbout: String?
    var askDraft = ""
    var focus: FocusRequest?
    var boardShown = true
    /// The "Run overnight" sheet, and the values a demo launch fills it with.
    var overnightShown = false
    var overnightSeed: OvernightLoop?
    /// When the window was last looked at, and the "While you were away" digest it gives.
    var away = AwayWatch()
    /// A fold a demo script asks for: `handled`, or `track:ID`.
    var foldRequest: FoldRequest?
    /// The targets a demo script holds hovered and pressed with no pointer; views compare their own `targetID` with them.
    var hoveredTarget: String?
    /// False while `make ui-check` records: a still shows the state its script sets, never the real pointer's (EMI-486).
    var pointerLive = true
    var pressedTarget: String?
    /// EMI-500 prototypes: how a quiet notice shows, and whether the picker shows (--lab, fixture only).
    var noticeStyle = NoticeStyle.banner
    var noticeLabShown = false
    /// The conversation entry to bring to the top.
    var jumpRequest: JumpRequest?
    /// The message a jump landed on, shaded for a moment.
    var landed: String?
    /// The messages of Prism's kept on the board, in the order kept. Held in memory only.
    var kept: [String] = []
    /// The message of Emil's the last step or mark went to; nil at the bottom.
    var askedCursor: String?
    /// Emil's messages on screen, for the rail.
    var askedVisible: Set<String> = []

    struct JumpRequest: Equatable {
        var entry: String
        var id = UUID()
    }

    func jump(to entry: String, landing: String? = nil) {
        jumpRequest = JumpRequest(entry: entry)
        landed = landing
        guard let landing else { return }
        Task {
            try? await Task.sleep(for: .milliseconds(1800))
            if landed == landing { landed = nil }
        }
    }

    /// Steps to the message of Emil's before (-1) or after (+1) the last one stepped to; past the newest is the bottom.
    func stepAsked(_ delta: Int, in s: Snapshot) {
        askedCursor = Answers.step(from: askedCursor, by: delta, in: Answers.asked(s.conversation).map(\.id))
        if let askedCursor { jump(to: askedCursor) } else { scroll("bottom") }
    }

    func toggleKept(_ id: String) {
        if let i = kept.firstIndex(of: id) { kept.remove(at: i) } else { kept.append(id) }
    }

    /// Shows a row pressed for a moment, as a digit key's echo.
    func flash(_ target: String) {
        pressedTarget = target
        Task {
            try? await Task.sleep(for: .milliseconds(140))
            if pressedTarget == target { pressedTarget = nil }
        }
    }

    struct FocusRequest: Equatable {
        var field: ComposerField
        var id = UUID()
    }

    struct FoldRequest: Equatable {
        var target: String
        var id = UUID()
    }

    /// Copies each dropped file into the tracks home before anything else: a screenshot's own file is temporary and
    /// unreadable to the desk. Returns whether anything was attached.
    @discardableResult
    func attach(_ urls: [URL]) -> Bool {
        var added = false
        for url in urls where url.isFileURL {
            guard let copy = DropStash.stash(url), !attachments.contains(copy) else { continue }
            attachments.append(copy)
            added = true
        }
        return added
    }

    func requestFocus(_ field: ComposerField) { focus = FocusRequest(field: field) }

    func fold(_ target: String) { foldRequest = FoldRequest(target: target) }

    /// The window is on screen: not covered, not in the Dock. Nothing animates while it is not.
    var windowVisible = true

    /// A demo script scrolls the conversation: `up` or `bottom`.
    var scrollRequest: ScrollRequest?

    struct ScrollRequest: Equatable {
        var direction: String
        var id = UUID()
    }

    func scroll(_ direction: String) { scrollRequest = ScrollRequest(direction: direction) }

    /// The session the middle column shows, if it shows one.
    func session(in s: Snapshot) -> Session? {
        switch selection {
        case .session(let id): s.session(id)
        case .track(let id): Sidebar(s).sections.flatMap(\.tracks).first { $0.id == id }?.primary
        case .desk, nil: nil
        }
    }

    /// The question enter, 1–9 and the composer answer.
    func activeQuestion(in s: Snapshot) -> Question? {
        if let id = pickedQuestion, let q = s.question(id), q.isOpen { return q }
        if questionDismissed { return nil }
        switch selection {
        case .desk, nil:
            return Board(s).needsYou.first
        case .track, .session:
            guard let session = session(in: s) else { return nil }
            return s.openQuestions.first { $0.session == session.id }
        }
    }

    /// The question 1–9, enter, space and m act on: in a track's dialog, the first one still undecided, so answering
    /// one moves the keys on to the next.
    func keyedQuestion(_ store: DeskStore) -> Question? {
        activeQuestion(in: store.snapshot).map(store.nextUndecided)
    }

    func pick(_ question: String) {
        pickedQuestion = question
        questionDismissed = false
    }

    func dismissQuestion() {
        pickedQuestion = nil
        questionDismissed = true
    }
}

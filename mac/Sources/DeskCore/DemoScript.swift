import Foundation

/// Timed changes played on the fixture (`--demo-script FILE`), so a motion recording is the same every run.
public struct DemoScript: Decodable, Equatable, Sendable {
    /// Quiet seconds before the first step: the checker learns from them what moves on its own.
    public var lead: Double
    /// Seconds after the last step before the run counts as over.
    public var tail: Double
    public var steps: [Step]

    public struct Step: Decodable, Equatable, Sendable {
        public var name: String
        /// Seconds after the lead.
        public var at: Double
        public var action: Action
        /// Seconds the code asks the change to take on screen; nil for a change with no animation.
        public var expect: Double?
        /// Changes on its own through the step (a countdown): its runs are not replays, only late frames inside them count.
        public var continuous: Bool
        /// Old and new show together on purpose, so it is not a double.
        public var crossfade: Bool
        /// Plays the action again this many seconds later inside the same step, as a change that fires twice would.
        public var again: Double?
        /// `make ui-check`: takes a still of the window under this name once the action has settled.
        public var shot: String?

        private enum CodingKeys: String, CodingKey { case name, at, `do`, event, events, direction, question, option, track, mode, fold, target, field, item, text, entry, expect, continuous, crossfade, again, shot }

        public init(name: String, at: Double, action: Action, expect: Double? = nil, continuous: Bool = false, crossfade: Bool = false, again: Double? = nil, shot: String? = nil) {
            self.name = name
            self.at = at
            self.action = action
            self.expect = expect
            self.continuous = continuous
            self.crossfade = crossfade
            self.again = again
            self.shot = shot
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = try c.decode(String.self, forKey: .name)
            at = try c.decode(Double.self, forKey: .at)
            expect = try c.decodeIfPresent(Double.self, forKey: .expect)
            continuous = try c.decodeIfPresent(Bool.self, forKey: .continuous) ?? false
            crossfade = try c.decodeIfPresent(Bool.self, forKey: .crossfade) ?? false
            again = try c.decodeIfPresent(Double.self, forKey: .again)
            shot = try c.decodeIfPresent(String.self, forKey: .shot)
            switch try c.decode(String.self, forKey: .do) {
            case "event": action = .event(try c.decode(StreamEvent.Change.self, forKey: .event))
            case "events": action = .events(try c.decode([StreamEvent.Change].self, forKey: .events))
            case "scroll": action = .scroll(try c.decode(String.self, forKey: .direction))
            case "pick": action = .pick(try c.decode(String.self, forKey: .question))
            case "answer": action = .answer(try c.decode(String.self, forKey: .question), option: try c.decodeIfPresent(Int.self, forKey: .option))
            case "undo": action = .undo(try c.decodeIfPresent(String.self, forKey: .question))
            case "mode": action = .mode(track: try c.decode(String.self, forKey: .track), mode: try c.decode(String.self, forKey: .mode))
            case "fold": action = .fold(try c.decode(String.self, forKey: .fold))
            case "hover": action = .hover(try c.decodeIfPresent(String.self, forKey: .target) ?? "")
            case "press": action = .press(try c.decodeIfPresent(String.self, forKey: .target) ?? "")
            case "focus": action = .focus(try c.decode(String.self, forKey: .field))
            case "select": action = .select(try c.decode(String.self, forKey: .item))
            case "dismiss": action = .dismiss
            case "draft": action = .draft(try c.decodeIfPresent(String.self, forKey: .text) ?? "")
            case "jump": action = .jump(try c.decode(String.self, forKey: .entry))
            case "keep": action = .keep(try c.decode(String.self, forKey: .entry))
            case "wait": action = .wait
            case let other: throw DecodingError.dataCorruptedError(forKey: .do, in: c, debugDescription: "unknown step \(other)")
            }
        }
    }

    public enum Action: Equatable, Sendable {
        /// A change as the server would send it.
        case event(StreamEvent.Change)
        /// Changes that arrive together, in one turn of the main actor.
        case events([StreamEvent.Change])
        /// Scrolls the conversation: `up` leaves the bottom, `bottom` goes back.
        case scroll(String)
        /// Loads a question into the composer, as a click on it would.
        case pick(String)
        /// Answers with option N (from 1), or the recommendation, starting the countdown.
        case answer(String, option: Int?)
        /// Takes back the pending answer to the question, or the newest one, as z does.
        case undo(String?)
        case mode(track: String, mode: String)
        /// `handled` and `landed` fold the board's lists, `track:ID` a track's sessions in the sidebar.
        case fold(String)
        /// The pointer rests on this target (`card:ID`, `row:desk`, `row:track:ID`, `row:session:ID`, `send`); empty moves it away.
        /// Set in the app, never by a real pointer.
        case hover(String)
        /// This target is held down; empty lets go.
        case press(String)
        /// Puts the cursor in a composer field: `desk`, `answer` or `ask`.
        case focus(String)
        /// Selects a sidebar row: `desk`, `track:ID` or `session:ID`.
        case select(String)
        /// Closes the question's card for the message field, as Escape does.
        case dismiss
        /// Sets the text of the message to the desk.
        case draft(String)
        /// EMI-488 lab: brings a conversation entry to the top, as a click on its mark or its board row does.
        case jump(String)
        /// EMI-488 lab: keeps a message of Prism's on the board, or lets it go.
        case keep(String)
        /// Changes nothing; the step names what happens on its own, such as a countdown running out.
        case wait
    }

    private enum CodingKeys: String, CodingKey { case lead, tail, steps }

    public init(lead: Double = 1, tail: Double = 1.5, steps: [Step]) {
        self.lead = lead
        self.tail = tail
        self.steps = steps
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        lead = try c.decodeIfPresent(Double.self, forKey: .lead) ?? 1
        tail = try c.decodeIfPresent(Double.self, forKey: .tail) ?? 1.5
        steps = try c.decode([Step].self, forKey: .steps)
        guard zip(steps, steps.dropFirst()).allSatisfy({ $0.at < $1.at }) else {
            throw DecodingError.dataCorruptedError(forKey: .steps, in: c, debugDescription: "steps must be in time order")
        }
    }

    public static func load(_ url: URL) throws -> DemoScript {
        try DeskJSON.decoder().decode(DemoScript.self, from: Data(contentsOf: url))
    }
}

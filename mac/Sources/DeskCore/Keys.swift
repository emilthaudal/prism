import Foundation

/// What a key press does in the window.
public enum KeyCommand: Equatable, Sendable {
    case takeRecommendation
    /// An option by its index, from 0.
    case pick(Int)
    case undo
    case takeOver
    case giveBack
    case dismissQuestion
    /// Quick Look on the open question's pictures.
    case preview
    /// Ask the agent about the open question instead of answering it.
    case askMore
    /// Put the cursor in the desk field.
    case focusDesk
    /// Send the staged answers now, past the last open question.
    case sendStaged
}

public struct KeyPress: Equatable, Sendable {
    public var characters: String
    public var control = false
    public var command = false
    public var option = false
    public var isReturn = false
    public var isEscape = false

    public init(characters: String, control: Bool = false, command: Bool = false, option: Bool = false, isReturn: Bool = false, isEscape: Bool = false) {
        self.characters = characters
        self.control = control
        self.command = command
        self.option = option
        self.isReturn = isReturn
        self.isEscape = isEscape
    }
}

public enum Keys {
    /// Bare keys, return and digits act on the open question only while no text field has the cursor; the fields keep
    /// their keys. ⌃1–⌃9 pick from a field, ⌘Z takes back an answer, ⌘L goes to the desk field and ⌘↩ sends the staged
    /// answers, anywhere.
    public static func command(for key: KeyPress, editing: Bool) -> KeyCommand? {
        if key.command {
            guard !key.control, !key.option else { return nil }
            if key.isReturn { return .sendStaged }
            switch key.characters.lowercased() {
            case "z": return .undo
            case "l": return .focusDesk
            default: return nil
            }
        }
        if key.option { return nil }
        let digit = key.characters.count == 1 ? Int(key.characters).flatMap { (1...9).contains($0) ? $0 - 1 : nil } : nil
        if key.control { return digit.map(KeyCommand.pick) }
        if key.isEscape { return .dismissQuestion }
        if editing { return nil }
        if key.isReturn { return .takeRecommendation }
        if let digit { return .pick(digit) }
        switch key.characters {
        case "z": return .undo
        case "t": return .takeOver
        case "g": return .giveBack
        case "m": return .askMore
        case " ": return .preview
        default: return nil
        }
    }
}

/// The text fields of the composer: the desk field is always there, the question's two sit on its card.
public enum ComposerField: Hashable, Sendable {
    case desk
    case answer
    case ask
}

public enum ComposerFocus {
    /// Where the cursor starts when a question's card appears: in its answer field when there is nothing to pick,
    /// otherwise nowhere, so the digits and return act on the question.
    public static func initial(for question: Question?) -> ComposerField? {
        guard let q = question, q.options.isEmpty, q.recommendationText == nil else { return nil }
        return .answer
    }

    /// The field a command moves the cursor to, if it does.
    public static func target(of command: KeyCommand, hasQuestion: Bool) -> ComposerField? {
        switch command {
        case .focusDesk: .desk
        case .askMore: hasQuestion ? .ask : nil
        default: nil
        }
    }
}

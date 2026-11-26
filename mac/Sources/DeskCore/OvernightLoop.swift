import Foundation

/// What the "Run overnight" sheet asks for, and the one message to the desk it becomes.
public struct OvernightLoop: Equatable, Sendable {
    public enum Until: String, CaseIterable, Identifiable, Sendable {
        case twoHours, fourHours, sevenOClock, eightHours

        public var id: String { rawValue }

        public var label: String {
            switch self {
            case .twoHours: "2 h"
            case .fourHours: "4 h"
            case .sevenOClock: "07:00"
            case .eightHours: "8 h"
            }
        }

        /// The flag the desk's Overnight loops section takes.
        public var flag: String {
            switch self {
            case .twoHours: "--for 2h"
            case .fourHours: "--for 4h"
            case .sevenOClock: "--until 07:00"
            case .eightHours: "--for 8h"
            }
        }

        /// 07:00 from 18:00 to 05:00, else a plain 8 h.
        public static func standard(at now: Date, calendar: Calendar = .current) -> Until {
            let hour = calendar.component(.hour, from: now)
            return hour >= 18 || hour < 5 ? .sevenOClock : .eightHours
        }
    }

    public static let turnChoices = [10, 20, 50]
    public static let budgetChoices = [50, 70, 90]

    public var goal = ""
    public var folder = ""
    public var until: Until
    public var turns = 20
    public var budget = 70

    public init(goal: String = "", folder: String = "", until: Until, turns: Int = 20, budget: Int = 70) {
        self.goal = goal
        self.folder = folder
        self.until = until
        self.turns = turns
        self.budget = budget
    }

    /// The goal and the folder are both required.
    public var isReady: Bool { !Self.oneLine(goal).isEmpty && !folder.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    /// The message to the desk. The goal is one line: newlines and runs of spaces become one space and double quotes
    /// become single ones, so the desk can pass it on as a quoted argument.
    public var message: String {
        let goal = Self.oneLine(goal).replacingOccurrences(of: "\"", with: "'")
        let folder = Self.oneLine(folder)
        return "Start an overnight loop. Goal: \(goal.trimmingTrailingPeriods()). Folder: \(folder). Limits: \(until.flag) --turns \(turns) --budget \(budget)."
    }

    /// The folder of a track whose "where" is a path, else nil: the snapshot has no other folder.
    public static func folder(of track: Track?) -> String? {
        guard let place = track?.where?.trimmingCharacters(in: .whitespaces), place.hasPrefix("/") || place.hasPrefix("~/") else { return nil }
        return place
    }

    static func oneLine(_ text: String) -> String {
        text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }
}

private extension String {
    func trimmingTrailingPeriods() -> String {
        var s = self
        while s.hasSuffix(".") { s.removeLast() }
        return s
    }
}

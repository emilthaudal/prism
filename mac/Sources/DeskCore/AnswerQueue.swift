import Foundation

/// An answer waiting out its undo window before it is sent.
public struct PendingAnswer: Equatable, Sendable {
    public var question: String
    public var choice: AnswerChoice
    /// What the countdown names: the option's label or the words.
    public var label: String
    public var deadline: Date
    /// The answer is not the recommendation, so the countdown says so while Emil can still undo it.
    public var againstRecommendation = false

    public func secondsLeft(at now: Date) -> Int {
        max(0, Int(deadline.timeIntervalSince(now).rounded(.up)))
    }
}

/// One dialog's staged answers waiting out one undo window; each asker in it gets its answers as one message.
public struct PendingBatch: Equatable, Sendable {
    public var dialog: String
    public var items: [BatchItem]
    public var deadline: Date

    public var count: Int { items.count }

    /// How many of the answers are not the recommendation.
    public var againstCount: Int { items.filter(\.againstRecommendation).count }

    public func contains(_ question: String) -> Bool { items.contains { $0.question == question } }

    public func secondsLeft(at now: Date) -> Int {
        max(0, Int(deadline.timeIntervalSince(now).rounded(.up)))
    }
}

/// The 5-second undo: answers wait here, newest last, until they are due or taken back.
public struct AnswerQueue: Equatable, Sendable {
    public static let undoWindow: TimeInterval = 5

    public private(set) var items: [PendingAnswer] = []
    public private(set) var batches: [PendingBatch] = []

    public init() {}

    public var isEmpty: Bool { items.isEmpty && batches.isEmpty }

    public func pending(for question: String) -> PendingAnswer? {
        items.first { $0.question == question }
    }

    public func batch(containing question: String) -> PendingBatch? {
        batches.first { $0.contains(question) }
    }

    /// Starts the countdown. A second answer to the same question replaces the first and starts again.
    @discardableResult
    public mutating func start(_ question: Question, choice: AnswerChoice, now: Date) -> PendingAnswer {
        let label = switch choice {
        case .option(let n): question.options.indices.contains(n) ? question.options[n].label : "option \(n + 1)"
        case .text(let text): text
        }
        let against = question.recommendedChoice.map { $0 != choice } ?? false
        let pending = PendingAnswer(question: question.id, choice: choice, label: label, deadline: now.addingTimeInterval(Self.undoWindow), againstRecommendation: against)
        items.removeAll { $0.question == question.id }
        items.append(pending)
        return pending
    }

    @discardableResult
    public mutating func startBatch(dialog: String, items answers: [BatchItem], now: Date) -> PendingBatch {
        let batch = PendingBatch(dialog: dialog, items: answers, deadline: now.addingTimeInterval(Self.undoWindow))
        batches.removeAll { $0.dialog == dialog }
        batches.append(batch)
        return batch
    }

    /// Takes back the answer to `question`, or the newest one when nil.
    @discardableResult
    public mutating func undo(_ question: String? = nil) -> PendingAnswer? {
        let index = if let question { items.lastIndex { $0.question == question } } else { items.indices.last }
        guard let i = index else { return nil }
        return items.remove(at: i)
    }

    /// Takes back the batch that holds `question`, or the newest batch when nil.
    @discardableResult
    public mutating func undoBatch(containing question: String? = nil) -> PendingBatch? {
        let index = if let question { batches.firstIndex { $0.contains(question) } } else { batches.indices.last }
        guard let i = index else { return nil }
        return batches.remove(at: i)
    }

    /// Whether the newest countdown is a batch, for an undo that names no question.
    public var newestIsBatch: Bool {
        guard let batch = batches.last else { return false }
        guard let single = items.last else { return true }
        return batch.deadline >= single.deadline
    }

    /// Removes and returns the answers whose window has passed, oldest first.
    public mutating func takeDue(now: Date) -> [PendingAnswer] {
        let due = items.filter { $0.deadline <= now }
        items.removeAll { $0.deadline <= now }
        return due
    }

    public mutating func takeDueBatches(now: Date) -> [PendingBatch] {
        let due = batches.filter { $0.deadline <= now }
        batches.removeAll { $0.deadline <= now }
        return due
    }

    /// Forgets answers to questions that are no longer open (answered elsewhere, or gone).
    public mutating func drop(where isGone: (String) -> Bool) {
        items.removeAll { isGone($0.question) }
        for i in batches.indices { batches[i].items.removeAll { isGone($0.question) } }
        batches.removeAll { $0.items.isEmpty }
    }
}

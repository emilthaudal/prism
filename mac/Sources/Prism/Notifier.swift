import AppKit
import DeskCore
import UserNotifications

/// A new question becomes a notification with its options as actions, the recommended one first. Picking one answers at
/// once: the notification is the confirmation, so there is no countdown.
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    private var categories: [String: UNNotificationCategory] = [:]
    private let store: DeskStore
    private let show: (String) -> Void
    private static let optionPrefix = "option-"

    init(store: DeskStore, show: @escaping (String) -> Void) {
        self.store = store
        self.show = show
        super.init()
    }

    /// Only a bundled app may use the notification center.
    static var available: Bool { Bundle.main.bundleIdentifier != nil }

    func start() {
        center.delegate = self
        Task { _ = try? await center.requestAuthorization(options: [.alert, .sound]) }
    }

    func post(_ q: Question, track: Track?) {
        let style = NotificationStyle.style(for: track)
        guard style != .queueOnly else { return } // the question is in the queue; no banner
        let track = track?.name
        let order = q.recommendedIndex.map { r in [r] + q.options.indices.filter { $0 != r } } ?? Array(q.options.indices)
        let actions = order.map { UNNotificationAction(identifier: "\(Self.optionPrefix)\($0)", title: q.options[$0].label, options: []) }
        let categoryID = "question-\(q.id)"
        categories[categoryID] = UNNotificationCategory(identifier: categoryID, actions: actions, intentIdentifiers: [])
        center.setNotificationCategories(Set(categories.values))

        let content = UNMutableNotificationContent()
        content.title = [q.id, track].compactMap { $0 }.joined(separator: " \u{00B7} ")
        var body = q.question.replacingOccurrences(of: "`", with: "")
        if let r = q.recommendedIndex {
            let option = q.options[r]
            body += " I recommend \(option.label.lowercased())" + (option.description.map { ": \($0)" } ?? ".")
        }
        content.body = body
        content.sound = style == .sound ? .default : nil
        content.categoryIdentifier = categoryID
        content.userInfo = ["question": q.id]
        center.add(UNNotificationRequest(identifier: q.id, content: content, trigger: nil))
    }

    func remove(_ questionID: String) {
        center.removeDeliveredNotifications(withIdentifiers: [questionID])
        center.removePendingNotificationRequests(withIdentifiers: [questionID])
        categories["question-\(questionID)"] = nil
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        // The sound is the notification's own: a silent banner has none to play.
        notification.request.content.sound == nil ? [.banner] : [.banner, .sound]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let action = response.actionIdentifier
        guard let id = response.notification.request.content.userInfo["question"] as? String else { return }
        await MainActor.run {
            if action.hasPrefix(Self.optionPrefix), let n = Int(action.dropFirst(Self.optionPrefix.count)) {
                Task { await store.answerNow(id, choice: .option(n)) }
            } else {
                show(id)
            }
        }
    }
}

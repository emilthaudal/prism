import Foundation

/// How a question reaches Emil, by the mode its track is shown in (EMI-423): focus with sound,
/// background as a silent banner, waiting and parked only in the queue.
public enum NotificationStyle: Equatable, Sendable {
    case sound
    case silentBanner
    case queueOnly

    /// A question with no track (a desk decision) is Emil's own and keeps its sound.
    public static func style(for track: Track?) -> NotificationStyle {
        switch track?.shown {
        case nil, "focus": .sound
        case "background": .silentBanner
        default: .queueOnly
        }
    }
}

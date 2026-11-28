import Foundation

/// Marks of Emil's messages that sit too close in the margin to be told apart, drawn and clicked as one.
public struct MarkGroup: Equatable, Sendable, Identifiable {
    /// The messages merged, oldest first.
    public var ids: [String]
    /// Where the group sits in the margin, from the top of the span: where its oldest mark would.
    public var y: Double
    /// The newest message of the group: a click goes there, and pointing names it.
    public var target: String { ids[ids.count - 1] }
    public var id: String { target }
}

public enum MarkGroups {
    /// Emil's messages placed by their index in the conversation over `span` points, merged while they sit within
    /// `minGap` of the group's first mark, so groups sit at least `minGap` apart.
    public static func group(_ conversation: [ConversationEntry], span: Double, minGap: Double) -> [MarkGroup] {
        let count = Double(conversation.count)
        var out: [(ids: [String], y: Double)] = []
        for (i, entry) in conversation.enumerated() where entry.role == "emil" {
            let y = span * (Double(i) + 0.5) / count
            if let open = out.last, y - open.y < minGap {
                out[out.count - 1].ids.append(entry.id)
            } else {
                out.append(([entry.id], y))
            }
        }
        return out.map { MarkGroup(ids: $0.ids, y: $0.y) }
    }
}

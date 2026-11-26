/// A track's lane colour in the Prism style: one of the five colours the icon's beam splits into, carried from the
/// track's row down to its sessions and subagents. Picked from the track's id, so a track keeps its colour across
/// launches, renames of other tracks and reordering.
public enum Lane: Int, CaseIterable, Sendable {
    case pink, amber, teal, blue, violet

    public static func of(_ trackID: String) -> Lane {
        // FNV-1a: Swift's own hashValue is seeded per launch, so it can't give a stable colour.
        var hash: UInt32 = 2_166_136_261
        for byte in trackID.utf8 {
            hash ^= UInt32(byte)
            hash = hash &* 16_777_619
        }
        return Lane(rawValue: Int(hash % UInt32(allCases.count)))!
    }
}

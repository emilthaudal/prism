import Testing
@testable import DeskCore

@Suite struct LaneTests {
    @Test func aTrackKeepsItsLane() {
        // Pinned values: a change to the hash would recolour every track Emil knows by its colour.
        #expect(Lane.of("recipe-search") == .blue)
        #expect(Lane.of("bun-rollout") == .amber)
        #expect(Lane.of("scanner-skips-shop-api") == .teal)
        #expect(Lane.of("prism-redesign") == .teal)
    }

    @Test func tracksSpreadOverAllFiveLanes() {
        let lanes = Set((0..<200).map { Lane.of("track-\($0)") })
        #expect(lanes.count == Lane.allCases.count)
    }
}

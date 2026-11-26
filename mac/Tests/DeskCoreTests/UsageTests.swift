import Foundation
import Testing
@testable import DeskCore

@Suite struct UsageTests {
    @Test func fixtureCarriesTheLimits() throws {
        let s = try loadFixture()
        let u = try #require(s.desk.usage)
        #expect(u.fiveHour == UsageLimit(usedPercentage: 42, resetsAt: DeskJSON.parseDate("2026-11-24T15:40:00Z")))
        #expect(u.sevenDay?.usedPercentage == 18)
        #expect(u.updatedAt == DeskJSON.parseDate("2026-11-24T13:31:00Z"))
    }

    @Test func aDeskWithoutUsageDecodes() throws {
        let json = #"{"seq": 1, "desk": {"session": "x", "status": "idle", "queued_events": 0, "usage": null}}"#
        let s = try DeskJSON.decoder().decode(Snapshot.self, from: Data(json.utf8))
        #expect(s.desk.usage == nil)
    }

    @Test func levels() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        let later = now.addingTimeInterval(3600)
        func level(_ p: Double) -> UsageLimit.Level { UsageLimit(usedPercentage: p, resetsAt: later).level(now: now) }
        #expect(level(0) == .plenty)
        #expect(level(74.9) == .plenty)
        #expect(level(75) == .close)
        #expect(level(90) == .nearlyOut)
        #expect(level(100) == .reached)

        // Past its reset, the limit has started over.
        let reset = UsageLimit(usedPercentage: 100, resetsAt: now.addingTimeInterval(-1))
        #expect(reset.level(now: now) == .plenty)
        #expect(reset.shownPercentage(now: now) == 0)
        #expect(UsageLimit(usedPercentage: 41.6).shownPercentage(now: now) == 42)
    }

    @Test func staleAfterTenMinutes() {
        let at = Date(timeIntervalSince1970: 1_000_000)
        let u = Usage(fiveHour: UsageLimit(usedPercentage: 1), updatedAt: at)
        #expect(!u.isStale(now: at.addingTimeInterval(600)))
        #expect(u.isStale(now: at.addingTimeInterval(601)))
    }
}

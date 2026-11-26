import Foundation
import Testing
@testable import DeskCore

@Suite struct OvernightLoopTests {
    private func loop(_ goal: String = "Get p95 under 120 ms", folder: String = "~/code/pantry-api", until: OvernightLoop.Until = .sevenOClock) -> OvernightLoop {
        OvernightLoop(goal: goal, folder: folder, until: until)
    }

    @Test func messageNamesTheGoalFolderAndLimits() {
        #expect(loop().message == "Start an overnight loop. Goal: Get p95 under 120 ms. Folder: ~/code/pantry-api. Limits: --until 07:00 --turns 20 --budget 70.")
    }

    @Test func eachUntilChoiceHasItsFlag() {
        let flags = OvernightLoop.Until.allCases.map { loop(until: $0).message.components(separatedBy: "Limits: ")[1] }
        #expect(flags == ["--for 2h --turns 20 --budget 70.", "--for 4h --turns 20 --budget 70.", "--until 07:00 --turns 20 --budget 70.", "--for 8h --turns 20 --budget 70."])
    }

    @Test func turnsAndBudgetGoIntoTheFlags() {
        var l = loop()
        l.turns = 50
        l.budget = 90
        #expect(l.message.hasSuffix("--until 07:00 --turns 50 --budget 90."))
    }

    @Test func aGoalIsOneTrimmedLine() {
        #expect(loop("  Shrink the bundle.\n\nKeep  tests green. ").message.contains("Goal: Shrink the bundle. Keep tests green. Folder"))
        #expect(loop("Make it \"fast\"\nand small").message.contains("Goal: Make it 'fast' and small. Folder"))
        #expect(loop("Make it fast.").message.contains("Goal: Make it fast. Folder"))
        #expect(!loop("a\nb", folder: " /tmp/x \n").message.contains("\n"))
    }

    @Test func goalAndFolderAreRequired() {
        #expect(loop().isReady)
        #expect(!loop("  \n ").isReady)
        #expect(!loop(folder: "  ").isReady)
    }

    @Test func untilDefaultsToSevenOClockInTheEvening() {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "UTC")!
        func at(_ hour: Int) -> Date { cal.date(from: DateComponents(year: 2026, month: 11, day: 26, hour: hour, minute: 30))! }
        #expect(OvernightLoop.Until.standard(at: at(18), calendar: cal) == .sevenOClock)
        #expect(OvernightLoop.Until.standard(at: at(23), calendar: cal) == .sevenOClock)
        #expect(OvernightLoop.Until.standard(at: at(4), calendar: cal) == .sevenOClock)
        #expect(OvernightLoop.Until.standard(at: at(5), calendar: cal) == .eightHours)
        #expect(OvernightLoop.Until.standard(at: at(17), calendar: cal) == .eightHours)
    }

    @Test func aTrackFolderIsOnlyAPath() {
        #expect(OvernightLoop.folder(of: Track(id: "1", name: "pantry", where: "~/code/pantry-api")) == "~/code/pantry-api")
        #expect(OvernightLoop.folder(of: Track(id: "1", name: "pantry", where: "Wave 1: build")) == nil)
        #expect(OvernightLoop.folder(of: nil) == nil)
    }
}

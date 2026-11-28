import Foundation
import Testing
@testable import MotionCheck

/// A run on a 4-pixel window filmed at 120 fps: `picture(t)` says what it shows at time t.
private func run(until end: Double = 2, ticks: [Double]? = nil, steps: [Timings.Step], still: [UInt8]? = nil, dropping: Range<Double>? = nil, picture: (Double) -> [UInt8]) -> MotionCheck.Report {
    let times = stride(from: 0.0, to: end, by: 1.0 / 120).filter { !(dropping?.contains($0) ?? false) }
    var pictures: [[UInt8]] = []
    let frames = times.map { t in
        pictures.append(picture(t))
        return Recording.Frame(t: t, picture: pictures.count - 1)
    }
    let appTicks = ticks ?? stride(from: 0.0, to: end, by: 1.0 / 120).map { $0 }
    let timings = Timings(leadStart: 0, end: end, steps: steps, ticks: appTicks)
    var check = MotionCheck()
    check.minPixels = 1
    return check.check(timings, Recording(width: 4, height: 1, scale: 100, frames: frames), pictures: pictures, still: still)
}

/// Fades the whole window from black to 200 between `from` and `to`.
private func fade(_ t: Double, from: Double, to: Double) -> [UInt8] {
    let share = min(max((t - from) / (to - from), 0), 1)
    return [UInt8](repeating: UInt8(200 * share), count: 4)
}

@Suite struct MotionCheckTests {
    @Test func aSmoothChangeIsOneRunAndPasses() {
        let report = run(steps: [Timings.Step(name: "fold", start: 1, expect: 0.2)]) { fade($0, from: 1.01, to: 1.15) }
        let step = report.steps[0]
        #expect(step.runs == 1)
        #expect(step.late == 0)
        #expect(step.duplicates == 0)
        #expect(step.doubles == 0)
        #expect(abs((step.duration ?? 0) - 0.14) < 0.02)
        #expect(!report.failed)
    }

    @Test func aChangeLongerOrMuchShorterThanItAsksForFails() {
        let long = run(steps: [Timings.Step(name: "fold", start: 1, expect: 0.2)]) { t in t < 1.16 ? fade(t, from: 1.01, to: 1.15) : fade(t, from: 1.16, to: 1.3).map { 200 - $0 } }
        #expect(long.steps[0].problems.contains("duration"))
        let none = run(steps: [Timings.Step(name: "fold", start: 1, expect: 0.2)]) { _ in [0, 0, 0, 0] }
        #expect(none.steps[0].problems == ["duration"])
        let jump = run(steps: [Timings.Step(name: "fold", start: 1, expect: 0)]) { fade($0, from: 1.01, to: 1.15) }
        #expect(jump.steps[0].problems == ["duration"])
        let right = run(steps: [Timings.Step(name: "fold", start: 1, expect: 0.2)]) { fade($0, from: 1.01, to: 1.15) }
        #expect(right.steps[0].problems.isEmpty)
    }

    @Test func aFadeThatAsksForMoreIsFineUnderReduceMotion() {
        let steps = [Timings.Step(name: "fold", start: 1, expect: 0.3)]
        let timings = Timings(leadStart: 0, end: 2, reduceMotion: true, steps: steps, ticks: stride(from: 0.0, to: 2, by: 1.0 / 120).map { $0 })
        let times = stride(from: 0.0, to: 2, by: 1.0 / 120).map { $0 }
        let pictures = times.map { fade($0, from: 1.01, to: 1.15) }
        let frames = times.indices.map { Recording.Frame(t: times[$0], picture: $0) }
        var check = MotionCheck()
        check.minPixels = 1
        let report = check.check(timings, Recording(width: 4, height: 1, scale: 100, frames: frames), pictures: pictures)
        #expect(report.steps[0].problems.isEmpty)
    }

    /// Runs one step under Reduce Motion on four pixels, the picture a function of time.
    private func reduced(_ picture: @escaping (Double) -> [UInt8]) -> MotionCheck.Report {
        let times = stride(from: 0.0, to: 2, by: 1.0 / 120).map { $0 }
        let timings = Timings(leadStart: 0, end: 2, reduceMotion: true, steps: [Timings.Step(name: "arrive", start: 1)], ticks: times)
        let frames = times.indices.map { Recording.Frame(t: times[$0], picture: $0) }
        var check = MotionCheck()
        check.minPixels = 1
        return check.check(timings, Recording(width: 4, height: 1, scale: 100, frames: frames), pictures: times.map(picture))
    }

    @Test func aJumpWithAFadeAfterItIsNotAMoveUnderReduceMotion() {
        // The layout jumps (pixel 0) in the first frame and a new row fades in (pixels 2 and 3).
        let report = reduced { t in
            let share = fade(t, from: 1.01, to: 1.15)[0]
            return t < 1.01 ? [200, 0, 0, 0] : [0, 0, share, share]
        }
        #expect(report.steps[0].moves == 0)
        #expect(!report.failed)
    }

    @Test func aSlideAfterTheJumpIsStillAMoveUnderReduceMotion() {
        let report = reduced { t in
            if t < 1.01 { return [200, 0, 0, 0] }
            if t < 1.03 { return [0, 0, 100, 100] }
            if t < 1.1 { return [0, 0, 200, 0] }
            return [0, 0, 200, 200]
        }
        #expect(report.steps[0].moves > 0)
        #expect(report.steps[0].problems.contains("moves under Reduce Motion"))
    }

    @Test func aChangeThatPlaysTwiceFails() {
        let report = run(steps: [Timings.Step(name: "fold", start: 1)]) { t in
            t < 1.05 ? fade(t, from: 1.01, to: 1.05) : fade(t, from: 1.4, to: 1.45).map { 200 - $0 }
        }
        #expect(report.steps[0].runs == 2)
        #expect(report.steps[0].problems == ["played 2 times"])
        #expect(report.failed)
    }

    @Test func aGapInTheFilmIsNotAStillWindow() {
        let report = run(steps: [Timings.Step(name: "fold", start: 1)], dropping: 1.06..<1.25) { fade($0, from: 1.01, to: 1.3) }
        #expect(report.steps[0].runs == 1)
        #expect(!report.failed)
    }

    @Test func aLateAppFrameIsAHitch() {
        var ticks = stride(from: 0.0, to: 2, by: 1.0 / 120).map { $0 }
        ticks.removeAll { $0 > 1.05 && $0 < 1.1 }
        let report = run(ticks: ticks, steps: [Timings.Step(name: "fold", start: 1, expect: 0.2)]) { fade($0, from: 1.01, to: 1.15) }
        #expect(report.steps[0].late == 1)
        #expect(report.steps[0].problems == ["hitch"])
    }

    @Test func aStillFrameInsideAMoveIsAHitch() {
        let report = run(steps: [Timings.Step(name: "fold", start: 1, expect: 0.2)]) { t in
            fade(t > 1.1 && t < 1.13 ? 1.1 : t, from: 1.01, to: 1.15)
        }
        #expect(report.steps[0].duplicates >= 2)
        #expect(report.steps[0].runs == 1)
        #expect(report.steps[0].problems == ["hitch"])
    }

    /// A dark card on a light window moves from the first pixel to the last and shows in both places for a few frames.
    private func twoPlaces(_ t: Double) -> [UInt8] {
        if t < 1.01 { return [0, 200, 200, 200] }
        if t < 1.03 { return [0, 200, 200, 0] }
        return [200, 200, 200, 0]
    }

    @Test func aCardInItsOldAndNewPlaceIsADouble() {
        let report = run(steps: [Timings.Step(name: "move", start: 1)], picture: twoPlaces)
        #expect(report.steps[0].doubles > 0)
        #expect(report.steps[0].problems.contains("double"))
    }

    @Test func aSlideOverAPlainBackgroundIsNotADouble() {
        // Half its pixels match the start and half the end, as two copies would, but only one card is on screen.
        let report = run(steps: [Timings.Step(name: "move", start: 1)]) { t in
            if t < 1.01 { return [0, 200, 200, 200] }
            if t < 1.03 { return [200, 0, 200, 200] }
            return [200, 200, 0, 200]
        }
        #expect(report.steps[0].doubles == 0)
    }

    @Test func aNamedCrossfadeIsNotADouble() {
        let report = run(steps: [Timings.Step(name: "move", start: 1, crossfade: true)], picture: twoPlaces)
        #expect(report.steps[0].doubles == 0)
    }

    @Test func aFadeIsNotAMoveButASlideIs() {
        let check = MotionCheck()
        let before: [UInt8] = [200, 0, 0, 0], after: [UInt8] = [0, 0, 0, 200]
        let all = [0, 1, 2, 3]
        #expect(!check.isMove([100, 0, 0, 100], before: before, after: after, all))
        #expect(check.isMove([0, 200, 0, 0], before: before, after: after, all))
    }

    @Test func whatMovesInTheLeadIsIgnored() {
        // Pixel 3 blinks all the time, like a spinner; the step changes nothing else.
        let report = run(steps: [Timings.Step(name: "nothing", start: 1)]) { t in
            [0, 0, 0, Int(t * 120) % 2 == 0 ? 0 : 255]
        }
        #expect(report.steps[0].runs == 0)
        #expect(report.steps[0].duration == nil)
        #expect(!report.failed)
    }

    @Test func anEndStateUnlikeTheStillFails() {
        let steps = [Timings.Step(name: "fold", start: 1)]
        #expect(run(steps: steps, still: [200, 200, 200, 200]) { fade($0, from: 1.01, to: 1.15) }.endClean == true)
        let report = run(steps: steps, still: [0, 0, 0, 0]) { fade($0, from: 1.01, to: 1.15) }
        #expect(report.endClean == false)
        #expect(report.failed)
    }

    @Test func aStepStillMovingAtTheNextFails() {
        let report = run(steps: [Timings.Step(name: "a", start: 1), Timings.Step(name: "b", start: 1.1)]) { fade($0, from: 1.01, to: 1.15) }
        #expect(report.steps[0].problems.contains("still moving at the next step"))
    }
}

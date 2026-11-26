import Foundation

/// Reads a motion run (the app's step times and frame ticks, the recorder's frames) and judges each step.
public struct MotionCheck: Sendable {
    /// A grey level difference above this is a change; below it is scaling noise.
    public var tolerance = 8
    /// Changes closer together than this are one run.
    public var runGap = 0.1
    /// Two frames at 60 Hz: a background window on a ProMotion display gets display link ticks up to about 25 ms apart
    /// while nothing is wrong, so only a longer interval is a frame the app itself was late for.
    public var lateInterval = 2.0 / 60
    /// A step must be still this long before the next one starts.
    public var settleMargin = 0.05
    /// Fewer changed pixels than this between two frames is noise, such as a pixel of antialiasing.
    public var minPixels = 6
    /// A frame whose change is under this share of the step's largest is a tail (the last sub-pixel stretch of an ease), not motion.
    public var tailShare = 0.1
    /// The shortest a visible change may be, as a share of what the code asks for: an ease's last stretch moves less than a picture pixel.
    /// The longest is what it asks for plus `lateInterval`, two 60 Hz frames.
    public var shortestShare = 0.4
    /// A run whose largest frame change is under this share of the step's largest is a side effect, not a replay.
    public var minorRun = 0.1
    /// A double shows more than either end does: its edges in the changed area add up to at least this share of the busier
    /// end's. A slide or a cross-fade never shows more than one end (about 0.9 to 1.03 on the fixture); two copies of a card
    /// show nearly both.
    public var doubleInk = 1.15
    /// The close, minimise and zoom buttons, in points from the top left: a screenshot draws them unlike the stream.
    static let windowControls = CGSize(width: 88, height: 36)

    public init() {}

    public struct StepResult: Equatable, Sendable {
        public var name: String
        /// Seconds from the step to the first change on screen; nil when nothing changed.
        public var lag: Double?
        /// Seconds from the first change to the last.
        public var duration: Double?
        public var expect: Double?
        public var late = 0
        public var duplicates = 0
        public var runs = 0
        public var doubles = 0
        /// Frames that moved something rather than fading it.
        public var moves = 0
        public var settled = true
        public var problems: [String] = []
        /// Every frame that changed, for `check --detail`.
        public var changes: [Change] = []
        /// The app's late frame intervals, in seconds.
        public var lateIntervals: [Double] = []
    }

    public struct Change: Equatable, Sendable {
        /// Seconds after the step started.
        public var at: Double
        public var pixels: Int
        /// The changed area in picture pixels: minX, minY, maxX, maxY.
        public var box: [Int]
    }

    public struct Report: Equatable, Sendable {
        public var steps: [StepResult]
        /// The last frame equals the still taken after the run; nil without a still.
        public var endClean: Bool?
        /// How the last frame differs from the still, if it does.
        public var endChange: Change?
        public var nominalInterval: Double
        public var failed: Bool { steps.contains { !$0.problems.isEmpty } || endClean == false }
    }

    public func check(_ timings: Timings, _ recording: Recording, pictures: [[UInt8]], still: [UInt8]? = nil) -> Report {
        let nominal = Self.nominalInterval(timings.ticks)
        let frames = recording.frames.sorted { $0.t < $1.t }
        let complete = frames.filter { !$0.idle }
        let firstStep = timings.steps.first?.start ?? timings.end
        // The recorder's frame interval: a change is only seen to start and end to within one of them.
        let film = Self.nominalInterval(frames.map(\.t))
        let mask = noiseMask(complete.filter { $0.t < firstStep }.map { pictures[$0.picture] }, width: recording.width, height: recording.height)

        var results: [StepResult] = []
        for (i, step) in timings.steps.enumerated() {
            let end = i + 1 < timings.steps.count ? timings.steps[i + 1].start : timings.end
            results.append(checkStep(step, until: end, frames: frames, pictures: pictures, mask: mask, width: recording.width, missed: missedFrames(timings), reduceMotion: timings.reduceMotion, film: film))
        }
        var endClean: Bool?
        var endChange: Change?
        if let still, let last = complete.last {
            var stillMask = mask
            let controlsWidth = Int(Self.windowControls.width / recording.scale), controlsHeight = Int(Self.windowControls.height / recording.scale)
            for y in 0..<min(controlsHeight, recording.height) {
                for x in 0..<min(controlsWidth, recording.width) { stillMask[y * recording.width + x] = true }
            }
            let box = changedBox(pictures[last.picture], still, stillMask, width: recording.width)
            endClean = (box?.pixels ?? 0) < minPixels
            endChange = box.map { Change(at: timings.end - last.t, pixels: $0.pixels, box: $0.box) }
        }
        return Report(steps: results, endClean: endClean, endChange: endChange, nominalInterval: nominal)
    }

    private func checkStep(_ step: Timings.Step, until end: Double, frames: [Recording.Frame], pictures: [[UInt8]], mask: [Bool], width: Int, missed: [(at: Double, by: Double)], reduceMotion: Bool, film: Double) -> StepResult {
        var r = StepResult(name: step.name, expect: step.expect)
        guard let startIndex = frames.lastIndex(where: { $0.t <= step.start && !$0.idle }) ?? frames.firstIndex(where: { !$0.idle }) else {
            r.problems.append("no frames")
            return r
        }
        let window = frames[startIndex...].filter { $0.t < end }
        let before = pictures[frames[startIndex].picture]

        // Each frame against the one before it.
        var previous = before
        var same: [Double] = []
        var sequence: [(t: Double, pixels: Int)] = []
        for f in window.dropFirst() {
            if f.idle {
                same.append(f.t)
                sequence.append((f.t, 0))
                continue
            }
            let picture = pictures[f.picture]
            if let box = changedBox(previous, picture, mask, width: width), box.pixels >= minPixels {
                r.changes.append(Change(at: f.t - step.start, pixels: box.pixels, box: box.box))
                sequence.append((f.t, box.pixels))
            } else {
                same.append(f.t)
                sequence.append((f.t, 0))
            }
            previous = picture
        }
        let after = previous

        let largest = sequence.map(\.pixels).max() ?? 0
        sequence = sequence.map { $0.pixels < Int(tailShare * Double(largest)) ? (t: $0.t, pixels: 0) : $0 }

        // A run far smaller than the step's main one (a scroller flashing at the edge) is not the change playing again.
        let allRuns = runs(sequence)
        let peak = allRuns.map(\.peak).max() ?? 0
        let runs = allRuns.filter { Double($0.peak) >= minorRun * Double(peak) }.map(\.span)
        r.runs = runs.count
        if let first = runs.first?.lowerBound, let last = runs.last?.upperBound {
            r.lag = first - step.start
            r.duration = last - first
            r.settled = last <= end - settleMargin
        }

        // A frame the app missed while this step moves on screen. The stall that makes the change itself ends about when
        // the change first shows, so it counts as lag; the recorder's clock and the display link's agree to a frame.
        let moving: [ClosedRange<Double>] = step.continuous ? allRuns.map(\.span) : runs.map { $0.lowerBound...max($0.upperBound, $0.lowerBound + (step.expect ?? 0)) }
        r.lateIntervals = missed.filter { m in moving.contains { $0.contains(m.at) && m.at > $0.lowerBound + Self.alignment } }.map(\.by)
        r.late = r.lateIntervals.count

        guard !step.continuous, let first = runs.first?.lowerBound, let last = runs.last?.upperBound else {
            r.problems = problems(r, step: step, reduceMotion: reduceMotion, film: film)
            return r
        }
        // Inside a run, a frame equal to the one before is a stall.
        r.duplicates = same.filter { t in t > first && t < last && runs.contains { $0.contains(t) } }.count

        let moved = (0..<before.count).filter { !mask[$0] && abs(Int(before[$0]) - Int(after[$0])) > tolerance }
        if !moved.isEmpty {
            let region = around(moved, width: width, count: before.count)
            let endInk = max(ink(before, region, width: width), ink(after, region, width: width))
            for f in window where !f.idle && f.t >= first && f.t < last {
                let picture = pictures[f.picture]
                if !step.crossfade, isDouble(picture, before: before, after: after, moved),
                   Double(ink(picture, region, width: width)) >= doubleInk * Double(endInk) { r.doubles += 1 }
                if isMove(picture, before: before, after: after, moved) { r.moves += 1 }
            }
        }
        r.problems = problems(r, step: step, reduceMotion: reduceMotion, film: film)
        return r
    }

    private func problems(_ r: StepResult, step: Timings.Step, reduceMotion: Bool, film: Double) -> [String] {
        var p: [String] = []
        if r.late > 0 || r.duplicates > 0 { p.append("hitch") }
        if !step.continuous && r.runs > 1 { p.append("played \(r.runs) times") }
        if r.doubles > 0 { p.append("double") }
        if reduceMotion && r.moves > 0 { p.append("moves under Reduce Motion") }
        if !r.settled { p.append("still moving at the next step") }
        if let expect = step.expect, !step.continuous {
            let dur = r.duration ?? 0
            // The film measures the change to within a frame of its own at each end: 8 ms at the recorder's usual 120 fps, 33 ms
            // when other work on the Mac holds it to 30.
            let tooLong = dur > expect + lateInterval + film
            let tooShort = !reduceMotion && expect > 0 && dur < expect * shortestShare - film
            if tooLong || tooShort { p.append("duration") }
        }
        return p
    }

    /// Groups changed frames into runs. A run ends only on frames that show nothing changing for `runGap`: the
    /// recorder sometimes delivers no frames at all for a while, and that is a gap in the film, not a still window.
    private func runs(_ sequence: [(t: Double, pixels: Int)]) -> [(span: ClosedRange<Double>, peak: Int)] {
        var out: [(span: ClosedRange<Double>, peak: Int)] = []
        var stillUntil: Double?
        for f in sequence {
            guard f.pixels > 0 else {
                if !out.isEmpty { stillUntil = f.t }
                continue
            }
            if let last = out.last, (stillUntil ?? last.span.upperBound) - last.span.upperBound < runGap {
                out[out.count - 1] = (last.span.lowerBound...f.t, max(last.peak, f.pixels))
            } else {
                out.append((f.t...f.t, f.pixels))
            }
            stillUntil = nil
        }
        return out
    }

    /// Some of the changed area already shows the end and some still shows the start, with little in between. On its own
    /// this also fits a slide over a plain background (the background matches one end or the other wherever the rows are
    /// not), so a frame is only counted when it also shows more than either end (`doubleInk`): an element in its old and
    /// its new place at once.
    func isDouble(_ f: [UInt8], before: [UInt8], after: [UInt8], _ moved: [Int]) -> Bool {
        var old = 0, new = 0
        for i in moved {
            let v = Int(f[i])
            if abs(v - Int(before[i])) <= tolerance { old += 1 } else if abs(v - Int(after[i])) <= tolerance { new += 1 }
        }
        let n = Double(moved.count)
        return Double(old) >= 0.2 * n && Double(new) >= 0.2 * n && Double(old + new) >= 0.9 * n
    }

    /// Not one blend of start and end: a fade has every changed pixel the same share of the way across.
    func isMove(_ f: [UInt8], before: [UInt8], after: [UInt8], _ moved: [Int]) -> Bool {
        var vw = 0.0, ww = 0.0
        for i in moved {
            let w = Double(after[i]) - Double(before[i])
            vw += (Double(f[i]) - Double(before[i])) * w
            ww += w * w
        }
        let alpha = ww > 0 ? vw / ww : 0
        var residual = 0.0
        for i in moved {
            let w = Double(after[i]) - Double(before[i])
            residual += abs(Double(f[i]) - Double(before[i]) - alpha * w)
        }
        return residual / Double(moved.count) > Double(tolerance) * 1.5
    }

    /// The pixels in `indices` and the ones next to them.
    func around(_ indices: [Int], width: Int, count: Int) -> [Int] {
        var inside = [Bool](repeating: false, count: count)
        for i in indices {
            let x = i % width
            for d in [0, -1, 1, -width, width] {
                let j = i + d
                if j >= 0, j < count, !(d == -1 && x == 0), !(d == 1 && x == width - 1) { inside[j] = true }
            }
        }
        return inside.indices.filter { inside[$0] }
    }

    /// How much edge a picture has over `region`: the grey steps to the right and down.
    func ink(_ p: [UInt8], _ region: [Int], width: Int) -> Int {
        var e = 0
        for i in region {
            if (i + 1) % width != 0, i + 1 < p.count { e += abs(Int(p[i + 1]) - Int(p[i])) }
            if i + width < p.count { e += abs(Int(p[i + width]) - Int(p[i])) }
        }
        return e
    }

    /// Pixels that change while the script is quiet (a spinner, a working session's activity), with a pixel round them.
    func noiseMask(_ quiet: [[UInt8]], width: Int, height: Int) -> [Bool] {
        var mask = [Bool](repeating: false, count: width * height)
        for (a, b) in zip(quiet, quiet.dropFirst()) {
            for i in 0..<mask.count where abs(Int(a[i]) - Int(b[i])) > tolerance {
                let x = i % width, y = i / width
                for dy in -1...1 {
                    for dx in -1...1 {
                        let nx = x + dx, ny = y + dy
                        if nx >= 0, ny >= 0, nx < width, ny < height { mask[ny * width + nx] = true }
                    }
                }
            }
        }
        return mask
    }

    func changedBox(_ a: [UInt8], _ b: [UInt8], _ mask: [Bool], width: Int) -> (pixels: Int, box: [Int])? {
        var n = 0, minX = Int.max, minY = Int.max, maxX = -1, maxY = -1
        for i in 0..<min(a.count, b.count) where !mask[i] && abs(Int(a[i]) - Int(b[i])) > tolerance {
            n += 1
            let x = i % width, y = i / width
            minX = min(minX, x); maxX = max(maxX, x); minY = min(minY, y); maxY = max(maxY, y)
        }
        return n == 0 ? nil : (n, [minX, minY, maxX, maxY])
    }

    func differing(_ a: [UInt8], _ b: [UInt8], _ mask: [Bool]) -> Int {
        var n = 0
        for i in 0..<min(a.count, b.count) where !mask[i] && abs(Int(a[i]) - Int(b[i])) > tolerance { n += 1 }
        return n
    }

    /// How far apart the recorder's and the app's idea of when a frame showed can be.
    static let alignment = 0.03

    /// Ticks that came more than `lateInterval` after the one before, and how long the gap was.
    public func missedFrames(_ t: Timings) -> [(at: Double, by: Double)] {
        zip(t.ticks, t.ticks.dropFirst()).compactMap { a, b in b - a > lateInterval ? (b, b - a) : nil }
    }

    /// The median of the app's frame intervals: 1/120 s on a ProMotion display.
    static func nominalInterval(_ ticks: [Double]) -> Double {
        let d = zip(ticks.dropFirst(), ticks).map { $0 - $1 }.sorted()
        return d.isEmpty ? 1.0 / 60 : d[d.count / 2]
    }
}

extension MotionCheck.Report {
    /// One line per step, then the end state.
    public var lines: [String] {
        let width = steps.map(\.name.count).max() ?? 0
        var out = steps.enumerated().map { i, s in
            let name = s.name.padding(toLength: width, withPad: " ", startingAt: 0)
            let dur = s.duration.map { ms($0) } ?? "no change"
            let asks = s.expect.map { " (asks \(ms($0)))" } ?? ""
            let lag = s.lag.map { "lag \(ms($0))  " } ?? ""
            let verdict = s.problems.isEmpty ? "ok" : "FAIL " + s.problems.joined(separator: ", ")
            return "\(String(format: "%2d", i + 1))  \(name)  \(lag)dur \(dur)\(asks)  late \(s.late)  dup \(s.duplicates)  runs \(s.runs)  doubles \(s.doubles)  moves \(s.moves)  \(verdict)"
        }
        switch endClean {
        case true?: out.append("end state matches the still: ok")
        case false?: out.append("end state differs from the still: FAIL")
        case nil: out.append("end state: no still to compare")
        }
        return out
    }

    private func ms(_ s: Double) -> String { "\(Int((s * 1000).rounded()))ms" }
}

import CoreMedia
import Foundation
import MotionCheck
import ScreenCaptureKit

// prism-motion record PID DIR: records the largest window of PID with ScreenCaptureKit, filtered to that window, so
// nothing is activated or brought forward, until the app writes DIR/timings.json; then takes a still of the end state.
// prism-motion check DIR: judges each scripted step and exits 1 when one hitches, plays twice or doubles.
// prism-motion ui-compare CAPTURES BASELINES SCRIPT LABEL: compares each of the script's stills in CAPTURES with the one of
// the same name in BASELINES (settings from the script's "ui" block), prints a line each and exits 1 on a difference.

/// Pictures are the window's point size divided by this: enough to see a row move, small enough to keep every frame.
let scaleDown = 4.0
let usage = "usage: prism-motion record PID DIR | prism-motion check DIR [--detail] | prism-motion ui-compare CAPTURES BASELINES SCRIPT LABEL"

func fail(_ message: String, code: Int32 = 1) -> Never {
    FileHandle.standardError.write(Data("prism-motion: \(message)\n".utf8))
    exit(code)
}

func grey(_ buffer: CVPixelBuffer, width: Int, height: Int) -> [UInt8] {
    CVPixelBufferLockBaseAddress(buffer, .readOnly)
    defer { CVPixelBufferUnlockBaseAddress(buffer, .readOnly) }
    guard let base = CVPixelBufferGetBaseAddress(buffer) else { return [] }
    let row = CVPixelBufferGetBytesPerRow(buffer)
    let w = min(width, CVPixelBufferGetWidth(buffer)), h = min(height, CVPixelBufferGetHeight(buffer))
    var out = [UInt8](repeating: 0, count: width * height)
    let p = base.assumingMemoryBound(to: UInt8.self)
    for y in 0..<h {
        for x in 0..<w {
            let o = y * row + x * 4 // BGRA
            out[y * width + x] = UInt8((Int(p[o + 2]) * 77 + Int(p[o + 1]) * 150 + Int(p[o]) * 29) >> 8)
        }
    }
    return out
}

final class Recorder: NSObject, SCStreamOutput, @unchecked Sendable {
    let width: Int, height: Int
    private let lock = NSLock()
    private var frames: [Recording.Frame] = []
    private var pictures: [[UInt8]] = []
    private var index: [Int: [Int]] = [:]

    init(width: Int, height: Int) {
        self.width = width
        self.height = height
    }

    var count: Int { lock.withLock { frames.count } }

    func stream(_ stream: SCStream, didOutputSampleBuffer buffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen,
              let info = (CMSampleBufferGetSampleAttachmentsArray(buffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]])?.first,
              let raw = info[.status] as? Int, let status = SCFrameStatus(rawValue: raw) else { return }
        let t = CMSampleBufferGetPresentationTimeStamp(buffer).seconds
        lock.withLock {
            switch status {
            case .complete:
                guard let pixels = CMSampleBufferGetImageBuffer(buffer) else { return }
                frames.append(Recording.Frame(t: t, picture: store(grey(pixels, width: width, height: height))))
            case .idle:
                if let last = frames.last { frames.append(Recording.Frame(t: t, idle: true, picture: last.picture)) }
            default: break
            }
        }
    }

    private func store(_ p: [UInt8]) -> Int {
        let h = p.hashValue
        if let found = index[h]?.first(where: { pictures[$0] == p }) { return found }
        pictures.append(p)
        index[h, default: []].append(pictures.count - 1)
        return pictures.count - 1
    }

    func write(to dir: URL) throws {
        try lock.withLock {
            try JSONEncoder().encode(Recording(width: width, height: height, scale: scaleDown, frames: frames)).write(to: dir.appendingPathComponent(RunFiles.frames))
            try Data(pictures.joined()).write(to: dir.appendingPathComponent(RunFiles.pictures))
        }
    }
}

func record(pid: Int32, dir: URL) async throws {
    // ScreenCaptureKit needs a WindowServer connection, which a command-line tool only makes when asked.
    _ = CGMainDisplayID()
    let fm = FileManager.default
    var window: SCWindow?
    for _ in 0..<50 where window == nil {
        let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
        window = content.windows.filter { $0.owningApplication?.processID == pid && $0.windowLayer == 0 }
            .max { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }
        if window == nil { try await Task.sleep(for: .milliseconds(200)) }
    }
    guard let window, window.frame.width >= scaleDown, window.frame.height >= scaleDown else { fail("no window for pid \(pid)") }
    let width = Int(window.frame.width / scaleDown), height = Int(window.frame.height / scaleDown)
    let config = SCStreamConfiguration()
    config.width = width
    config.height = height
    config.pixelFormat = kCVPixelFormatType_32BGRA
    config.minimumFrameInterval = CMTime(value: 1, timescale: 120)
    config.showsCursor = false
    config.queueDepth = 8
    let filter = SCContentFilter(desktopIndependentWindow: window)
    let recorder = Recorder(width: width, height: height)
    let stream = SCStream(filter: filter, configuration: config, delegate: nil)
    try stream.addStreamOutput(recorder, type: .screen, sampleHandlerQueue: DispatchQueue(label: "prism-motion.frames"))
    do {
        try await stream.startCapture()
    } catch {
        fail("can't record window \(window.windowID) (\(window.frame)): \(error)")
    }
    for _ in 0..<100 where recorder.count == 0 { try await Task.sleep(for: .milliseconds(20)) }
    fm.createFile(atPath: dir.appendingPathComponent(RunFiles.recording).path, contents: nil)

    let timings = dir.appendingPathComponent(RunFiles.timings)
    let deadline = Date().addingTimeInterval(180)
    while !fm.fileExists(atPath: timings.path) {
        guard Date() < deadline, kill(pid, 0) == 0 else { try? await stream.stopCapture(); fail("the app ended or wrote no timings") }
        try await Task.sleep(for: .milliseconds(100))
    }
    try await stream.stopCapture()
    try recorder.write(to: dir)

    let still = try await SCScreenshotManager.captureSampleBuffer(contentFilter: filter, configuration: config)
    guard let pixels = CMSampleBufferGetImageBuffer(still) else { fail("no still") }
    try Data(grey(pixels, width: width, height: height)).write(to: dir.appendingPathComponent(RunFiles.still))
    print("recorded \(recorder.count) frames at \(width)x\(height) into \(dir.path)")
}

func check(dir: URL, detail: Bool) throws -> Bool {
    let timings = try JSONDecoder().decode(Timings.self, from: Data(contentsOf: dir.appendingPathComponent(RunFiles.timings)))
    let recording = try JSONDecoder().decode(Recording.self, from: Data(contentsOf: dir.appendingPathComponent(RunFiles.frames)))
    let size = recording.width * recording.height
    let blob = [UInt8](try Data(contentsOf: dir.appendingPathComponent(RunFiles.pictures)))
    let pictures = stride(from: 0, to: blob.count, by: size).map { Array(blob[$0..<min($0 + size, blob.count)]) }
    let still = (try? Data(contentsOf: dir.appendingPathComponent(RunFiles.still))).map { [UInt8]($0) }
    let report = MotionCheck().check(timings, recording, pictures: pictures, still: still)
    let fps = 1 / report.nominalInterval
    let recorded = recording.frames.filter { !$0.idle && $0.t >= timings.leadStart }
    let span = (recorded.last?.t ?? 0) - (recorded.first?.t ?? 0)
    print(String(format: "app frames %.0f fps · recorded %d frames (%.0f fps while changing or not)%@", fps, recorded.count, span > 0 ? Double(recorded.count) / span : 0, timings.reduceMotion ? " · Reduce Motion on" : ""))
    report.lines.forEach { print($0) }
    if detail {
        let leadLate = MotionCheck().missedFrames(timings).filter { $0.at < (timings.steps.first?.start ?? timings.end) }.map(\.by)
        print("lead: late \(leadLate.map { Int(($0 * 1000).rounded()) }) ms")
        if let c = report.endChange { print("end: \(c.pixels) px differ from the still, box \(c.box.map(String.init).joined(separator: ","))") }
        for s in report.steps {
            print("\(s.name): late \(s.lateIntervals.map { Int(($0 * 1000).rounded()) }) ms")
            for c in s.changes { print(String(format: "  +%4.0fms  %6d px  box %@", c.at * 1000, c.pixels, c.box.map(String.init).joined(separator: ","))) }
        }
    }
    return !report.failed
}

/// The parts of a demo script the comparison reads.
struct UIScript: Decodable {
    struct Step: Decodable { var name: String; var shot: String? }
    var steps: [Step]
    var ui: ImageCheck.Settings?
}

func uiCompare(captures: URL, baselines: URL, script: URL, label: String) throws -> Bool {
    let parsed = try JSONDecoder().decode(UIScript.self, from: Data(contentsOf: script))
    let check = ImageCheck(settings: parsed.ui ?? ImageCheck.Settings())
    var ok = true
    for step in parsed.steps {
        guard let shot = step.shot else { continue }
        let name = step.name
        let current = captures.appendingPathComponent("\(shot).png")
        let baseline = baselines.appendingPathComponent("\(shot).png")
        guard let now = ImageCheck.load(current) else {
            print("\(label)  \(shot)  (\(name))  FAIL no capture"); ok = false; continue
        }
        guard let was = ImageCheck.load(baseline) else {
            print("\(label)  \(shot)  (\(name))  FAIL no baseline at \(baseline.path) (make ui-baseline)"); ok = false; continue
        }
        let r = check.compare(was, now)
        if r.sizeChanged {
            print("\(label)  \(shot)  (\(name))  FAIL size \(now.width)x\(now.height) against baseline \(was.width)x\(was.height)"); ok = false; continue
        }
        let share = String(format: "%.3f%%", r.share * 100)
        guard check.failed(r) else {
            print("\(label)  \(shot)  (\(name))  ok  \(share) changed"); continue
        }
        let diff = captures.appendingPathComponent("\(shot).diff.png")
        let side = captures.appendingPathComponent("\(shot).side.png")
        let marked = check.diff(was, now)
        _ = ImageCheck.write(marked, to: diff)
        _ = ImageCheck.write(ImageCheck.sideBySide([was, now, marked]), to: side)
        print("\(label)  \(shot)  (\(name))  FAIL \(share) changed (\(r.changed) px), box \(r.box.map(String.init).joined(separator: ",")) (x,y,w,h), diff \(diff.path), side by side \(side.path)")
        ok = false
    }
    return ok
}

let args = Array(CommandLine.arguments.dropFirst())
switch (args.first, args.count) {
case ("record", 3):
    guard let pid = Int32(args[1]) else { fail(usage, code: 2) }
    do { try await record(pid: pid, dir: URL(fileURLWithPath: args[2])) } catch { fail("\(error)") }
case ("check", 2), ("check", 3):
    let detail = args.count == 3 && args[2] == "--detail"
    guard args.count == 2 || detail else { fail(usage, code: 2) }
    do { exit(try check(dir: URL(fileURLWithPath: args[1]), detail: detail) ? 0 : 1) } catch { fail("\(error)") }
case ("ui-compare", 5):
    do { exit(try uiCompare(captures: URL(fileURLWithPath: args[1]), baselines: URL(fileURLWithPath: args[2]), script: URL(fileURLWithPath: args[3]), label: args[4]) ? 0 : 1) } catch { fail("\(error)") }
default:
    fail(usage, code: 2)
}

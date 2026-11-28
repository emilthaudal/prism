import Foundation

/// What the app writes at the end of a motion run: when each step started and the frame times it saw, on the host
/// clock (`CACurrentMediaTime`), which is also the clock of ScreenCaptureKit's frame timestamps.
public struct Timings: Codable, Equatable, Sendable {
    /// When the quiet lead began; the steps start after it.
    public var leadStart: Double
    public var end: Double
    public var reduceMotion: Bool
    public var steps: [Step]
    /// The display link's frame times inside the app.
    public var ticks: [Double]

    public struct Step: Codable, Equatable, Sendable {
        public var name: String
        public var start: Double
        public var expect: Double?
        /// Changes on its own through the step, as a countdown does.
        public var continuous: Bool
        public var crossfade: Bool
        /// Late frames this step may show before it fails; nil allows none.
        public var lateFrames: Int?
        /// The longest this step may take on screen, in milliseconds; nil allows what `expect` asks for plus two 60 Hz frames.
        public var maxMs: Double?

        public init(name: String, start: Double, expect: Double? = nil, continuous: Bool = false, crossfade: Bool = false, lateFrames: Int? = nil, maxMs: Double? = nil) {
            self.name = name
            self.start = start
            self.expect = expect
            self.continuous = continuous
            self.crossfade = crossfade
            self.lateFrames = lateFrames
            self.maxMs = maxMs
        }
    }

    public init(leadStart: Double, end: Double, reduceMotion: Bool = false, steps: [Step], ticks: [Double]) {
        self.leadStart = leadStart
        self.end = end
        self.reduceMotion = reduceMotion
        self.steps = steps
        self.ticks = ticks
    }
}

/// The recorder's frames: small greyscale pictures of the window, each distinct one stored once.
public struct Recording: Codable, Equatable, Sendable {
    public var width: Int
    public var height: Int
    /// Window points per picture pixel.
    public var scale: Double
    public var frames: [Frame]

    public struct Frame: Codable, Equatable, Sendable {
        public var t: Double
        /// ScreenCaptureKit said nothing changed since the last frame.
        public var idle: Bool
        /// Which stored picture this frame shows.
        public var picture: Int

        public init(t: Double, idle: Bool = false, picture: Int) {
            self.t = t
            self.idle = idle
            self.picture = picture
        }
    }

    public init(width: Int, height: Int, scale: Double = 1, frames: [Frame]) {
        self.width = width
        self.height = height
        self.scale = scale
        self.frames = frames
    }
}

/// The files of a motion run in one directory.
public enum RunFiles {
    public static let timings = "timings.json"
    public static let frames = "frames.json"
    public static let pictures = "pictures.bin"
    public static let still = "end.gray"
    /// Written by the recorder once frames flow; the app waits for it before the lead starts.
    public static let recording = "recording"
}

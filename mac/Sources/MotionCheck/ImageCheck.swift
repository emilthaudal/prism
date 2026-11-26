import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

/// Compares a still of the window with its baseline (make ui-check): the pixels that differ by more than a tolerance,
/// outside the masked regions, as a share of the window.
public struct ImageCheck: Sendable {
    /// Named in the demo script's "ui" block, never fixed here: glass and the gradient differ a little between runs.
    public struct Settings: Codable, Equatable, Sendable {
        /// The largest difference in any colour channel (0 to 255) that still counts as the same pixel.
        public var tolerance: Int
        /// The share of unmasked pixels that may differ before the step fails.
        public var threshold: Double
        /// How far in picture pixels a pixel may have moved: text and edges laid out half a point apart from one launch to the
        /// next are the same picture. 0 compares each pixel with its own place only.
        public var shift: Int
        public var masks: [Mask]

        public init(tolerance: Int = 8, threshold: Double = 0.001, shift: Int = 0, masks: [Mask] = []) {
            self.tolerance = tolerance
            self.threshold = threshold
            self.shift = shift
            self.masks = masks
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            tolerance = try c.decodeIfPresent(Int.self, forKey: .tolerance) ?? 8
            threshold = try c.decodeIfPresent(Double.self, forKey: .threshold) ?? 0.001
            shift = try c.decodeIfPresent(Int.self, forKey: .shift) ?? 0
            masks = try c.decodeIfPresent([Mask].self, forKey: .masks) ?? []
        }
    }

    /// A region left out of the comparison, as shares of the window's width and height (0 to 1) from its top left.
    public struct Mask: Codable, Equatable, Sendable {
        public var name: String
        public var x: Double
        public var y: Double
        public var w: Double
        public var h: Double

        public init(name: String, x: Double, y: Double, w: Double, h: Double) {
            self.name = name
            self.x = x
            self.y = y
            self.w = w
            self.h = h
        }
    }

    /// 8-bit RGBA, rows from the top.
    public struct Picture: Equatable, Sendable {
        public var width: Int
        public var height: Int
        public var pixels: [UInt8]

        public init(width: Int, height: Int, pixels: [UInt8]) {
            self.width = width
            self.height = height
            self.pixels = pixels
        }
    }

    public struct Result: Equatable, Sendable {
        /// The pictures differ in size, so nothing was compared.
        public var sizeChanged = false
        public var changed = 0
        public var compared = 0
        /// x, y, width, height of the changed pixels in picture pixels; empty when nothing changed.
        public var box: [Int] = []
        public var share: Double { compared == 0 ? 0 : Double(changed) / Double(compared) }
    }

    public var settings: Settings

    public init(settings: Settings = Settings()) { self.settings = settings }

    public func failed(_ r: Result) -> Bool { r.sizeChanged || r.share > settings.threshold }

    public func compare(_ a: Picture, _ b: Picture) -> Result {
        guard a.width == b.width, a.height == b.height else { return Result(sizeChanged: true) }
        let skip = masked(width: a.width, height: a.height)
        var r = Result()
        var (x0, y0, x1, y1) = (a.width, a.height, -1, -1)
        for y in 0..<a.height {
            for x in 0..<a.width where !skip[y * a.width + x] {
                r.compared += 1
                let o = (y * a.width + x) * 4
                if differs(a, b, x, y) {
                    r.changed += 1
                    x0 = min(x0, x); y0 = min(y0, y); x1 = max(x1, x); y1 = max(y1, y)
                }
            }
        }
        if r.changed > 0 { r.box = [x0, y0, x1 - x0 + 1, y1 - y0 + 1] }
        return r
    }

    /// The current picture dimmed, with every differing pixel in red and the masked regions in blue.
    public func diff(_ a: Picture, _ b: Picture) -> Picture {
        guard a.width == b.width, a.height == b.height else { return b }
        let skip = masked(width: a.width, height: a.height)
        var out = b
        for i in 0..<(a.width * a.height) {
            let o = i * 4
            if skip[i] {
                (out.pixels[o], out.pixels[o + 1], out.pixels[o + 2]) = (40, 80, 255)
            } else if differs(a, b, i % a.width, i / a.width) {
                (out.pixels[o], out.pixels[o + 1], out.pixels[o + 2]) = (255, 0, 0)
            } else {
                for k in 0..<3 { out.pixels[o + k] = UInt8(128 + Int(b.pixels[o + k]) / 2) }
            }
        }
        return out
    }

    /// The baseline, the current picture and the diff next to each other.
    public static func sideBySide(_ pictures: [Picture]) -> Picture {
        let gap = 8
        let height = pictures.map(\.height).max() ?? 0
        let width = pictures.map(\.width).reduce(0, +) + gap * max(pictures.count - 1, 0)
        var out = [UInt8](repeating: 128, count: width * height * 4)
        var left = 0
        for p in pictures {
            for y in 0..<p.height {
                let from = y * p.width * 4
                let to = (y * width + left) * 4
                out.replaceSubrange(to..<(to + p.width * 4), with: p.pixels[from..<(from + p.width * 4)])
            }
            left += p.width + gap
        }
        return Picture(width: width, height: height, pixels: out)
    }

    /// A pixel differs when it matches nothing within `shift` pixels of its place in the other picture, and nothing of the
    /// other picture there matches it.
    private func differs(_ a: Picture, _ b: Picture, _ x: Int, _ y: Int) -> Bool {
        if !apart(a, x, y, b, x, y) { return false }
        let s = settings.shift
        guard s > 0 else { return true }
        for dy in -s...s {
            for dx in -s...s where dx != 0 || dy != 0 {
                let nx = x + dx, ny = y + dy
                guard nx >= 0, ny >= 0, nx < a.width, ny < a.height else { continue }
                if !apart(a, x, y, b, nx, ny) || !apart(b, x, y, a, nx, ny) { return false }
            }
        }
        return true
    }

    private func apart(_ p: Picture, _ px: Int, _ py: Int, _ q: Picture, _ qx: Int, _ qy: Int) -> Bool {
        let o = (py * p.width + px) * 4, n = (qy * q.width + qx) * 4
        for k in 0..<3 where abs(Int(p.pixels[o + k]) - Int(q.pixels[n + k])) > settings.tolerance { return true }
        return false
    }

    private func masked(width: Int, height: Int) -> [Bool] {
        var skip = [Bool](repeating: false, count: width * height)
        for m in settings.masks {
            let x0 = max(0, Int((m.x * Double(width)).rounded(.down))), x1 = min(width, Int(((m.x + m.w) * Double(width)).rounded(.up)))
            let y0 = max(0, Int((m.y * Double(height)).rounded(.down))), y1 = min(height, Int(((m.y + m.h) * Double(height)).rounded(.up)))
            guard x0 < x1, y0 < y1 else { continue }
            for y in y0..<y1 { for x in x0..<x1 { skip[y * width + x] = true } }
        }
        return skip
    }

    public static func load(_ url: URL) -> Picture? {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, nil), let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else { return nil }
        let w = image.width, h = image.height
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        let drawn = pixels.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(data: buffer.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            context.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        return drawn ? Picture(width: w, height: h, pixels: pixels) : nil
    }

    public static func write(_ picture: Picture, to url: URL) -> Bool {
        var pixels = picture.pixels
        let image: CGImage? = pixels.withUnsafeMutableBytes { buffer in
            CGContext(data: buffer.baseAddress, width: picture.width, height: picture.height, bitsPerComponent: 8, bytesPerRow: picture.width * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)?.makeImage()
        }
        guard let image, let dest = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil) else { return false }
        CGImageDestinationAddImage(dest, image, nil)
        return CGImageDestinationFinalize(dest)
    }
}

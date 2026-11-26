import Foundation
import Testing
@testable import MotionCheck

/// An 8x8 picture: grey 100 everywhere, with a 3x3 patch of `patch` at (2, 2).
private func picture(patch: UInt8 = 100, at x0: Int = 2, size: Int = 8) -> ImageCheck.Picture {
    var pixels = [UInt8](repeating: 100, count: size * size * 4)
    for i in 0..<(size * size) { pixels[i * 4 + 3] = 255 }
    for y in 0..<3 { for x in 0..<3 {
        let o = ((2 + y) * size + x0 + x) * 4
        for k in 0..<3 { pixels[o + k] = patch }
    } }
    return ImageCheck.Picture(width: size, height: size, pixels: pixels)
}

@Suite struct ImageCheckTests {
    @Test func identicalPicturesPass() {
        let check = ImageCheck(settings: .init(tolerance: 4, threshold: 0))
        let r = check.compare(picture(), picture())
        #expect(r.changed == 0)
        #expect(!check.failed(r))
    }

    @Test func aShadeAboveTheToleranceFailsAndNamesItsBox() {
        let check = ImageCheck(settings: .init(tolerance: 4, threshold: 0.01))
        let r = check.compare(picture(), picture(patch: 120))
        #expect(r.changed == 9)
        #expect(r.box == [2, 2, 3, 3])
        #expect(check.failed(r))
        #expect(!check.failed(check.compare(picture(), picture(patch: 103))))
    }

    @Test func aFewChangedPixelsUnderTheThresholdPass() {
        let check = ImageCheck(settings: .init(tolerance: 4, threshold: 0.2))
        #expect(!check.failed(check.compare(picture(), picture(patch: 120))))
    }

    @Test func aMaskedRegionIsLeftOut() {
        let mask = ImageCheck.Mask(name: "patch", x: 0.25, y: 0.25, w: 0.5, h: 0.5)
        let check = ImageCheck(settings: .init(tolerance: 4, threshold: 0, masks: [mask]))
        let r = check.compare(picture(), picture(patch: 120))
        #expect(r.changed == 0)
        #expect(r.compared < 64)
    }

    @Test func shiftLetsAnEdgeMoveByAPixelButNotAShade() {
        let moved = ImageCheck(settings: .init(tolerance: 4, threshold: 0, shift: 1))
        #expect(!moved.failed(moved.compare(picture(patch: 200), picture(patch: 200, at: 3))))
        let strict = ImageCheck(settings: .init(tolerance: 4, threshold: 0))
        #expect(strict.failed(strict.compare(picture(patch: 200), picture(patch: 200, at: 3))))
        #expect(moved.failed(moved.compare(picture(patch: 100), picture(patch: 120))))
    }

    @Test func differentSizesFail() {
        let check = ImageCheck()
        let r = check.compare(picture(size: 8), picture(size: 6))
        #expect(r.sizeChanged)
        #expect(check.failed(r))
    }

    @Test func theDiffMarksChangedPixelsRedAndMaskedBlue() {
        let mask = ImageCheck.Mask(name: "corner", x: 0, y: 0, w: 0.125, h: 0.125)
        let check = ImageCheck(settings: .init(tolerance: 4, threshold: 0, masks: [mask]))
        let marked = check.diff(picture(), picture(patch: 120))
        let at = { (x: Int, y: Int) in Array(marked.pixels[((y * 8 + x) * 4)...((y * 8 + x) * 4 + 2)]) }
        #expect(at(3, 3) == [255, 0, 0])
        #expect(at(0, 0) == [40, 80, 255])
        #expect(at(7, 7) != [255, 0, 0])
    }

    @Test func aPictureRoundTripsThroughPNG() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("image-check-\(UUID().uuidString).png")
        defer { try? FileManager.default.removeItem(at: url) }
        #expect(ImageCheck.write(picture(patch: 200), to: url))
        let back = try #require(ImageCheck.load(url))
        #expect(back == picture(patch: 200))
    }
}

import AppKit
import Foundation
import Testing
@testable import DeskCore

private final class Writer: NSObject, NSFilePromiseProviderDelegate {
    func filePromiseProvider(_ provider: NSFilePromiseProvider, fileNameForType fileType: String) -> String { "Screenshot.png" }
    func filePromiseProvider(_ provider: NSFilePromiseProvider, writePromiseTo url: URL, completionHandler: @escaping (Error?) -> Void) {
        do { try Data([0x89, 0x50, 0x4E, 0x47, 1, 2, 3]).write(to: url); completionHandler(nil) } catch { completionHandler(error) }
    }
}

@Suite struct DropPromiseTests {
    private func tmp() -> URL {
        FileManager.default.temporaryDirectory.appendingPathComponent("droppromise-\(UUID().uuidString)")
    }

    /// AppKit refuses to receive a promise outside a drag session, so the promise is received through the seam: the
    /// sender's delegate writes the file where the receiver would have asked it to.
    private func receive(_ pb: NSPasteboard, _ dir: URL, writer: Writer? = nil) async -> [URL] {
        await withCheckedContinuation { c in
            DropStash.receive(from: pb, in: dir, receivePromise: { _, dest, queue, reader in
                queue.addOperation {
                    let file = dest.appendingPathComponent("Screenshot.png")
                    writer?.filePromiseProvider(NSFilePromiseProvider(), writePromiseTo: file) { reader(file, $0) }
                }
            }) { c.resume(returning: $0) }
        }
    }

    @Test func aFilePromiseIsWrittenIntoTheDropsFolder() async throws {
        let dir = tmp().appendingPathComponent("drops")
        let writer = Writer()
        let provider = NSFilePromiseProvider(fileType: "public.png", delegate: writer)
        let pb = NSPasteboard(name: NSPasteboard.Name("test-\(UUID().uuidString)"))
        pb.clearContents()
        pb.writeObjects([provider])
        let urls = await receive(pb, dir, writer: writer)
        let copy = try #require(urls.first)
        #expect(copy.deletingLastPathComponent().standardizedFileURL == dir.standardizedFileURL)
        #expect(copy.lastPathComponent.hasSuffix("Screenshot.png"))
        #expect(try Data(contentsOf: copy).count == 7)
        withExtendedLifetime(writer) {}
    }

    @Test func imageDataIsWrittenAsPng() async throws {
        let dir = tmp().appendingPathComponent("drops")
        let image = NSImage(size: NSSize(width: 4, height: 4), flipped: false) { r in NSColor.red.setFill(); r.fill(); return true }
        let pb = NSPasteboard(name: NSPasteboard.Name("test-\(UUID().uuidString)"))
        pb.clearContents()
        pb.setData(image.tiffRepresentation, forType: .tiff)
        let urls = await receive(pb, dir)
        let copy = try #require(urls.first)
        #expect(copy.pathExtension == "png")
        #expect(NSImage(contentsOf: copy) != nil)
    }
}

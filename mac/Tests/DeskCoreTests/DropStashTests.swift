import Foundation
import Testing
@testable import DeskCore

@Suite struct DropStashTests {
    private func scratch() throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("dropstash-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    @Test func copiesTheDroppedFileSoItOutlivesTheOriginal() throws {
        let root = try scratch()
        defer { try? FileManager.default.removeItem(at: root) }
        let original = root.appendingPathComponent("Screenshot 1.png")
        try Data("png".utf8).write(to: original)
        let drops = root.appendingPathComponent("drops")

        let copy = try #require(DropStash.stash(original, in: drops))
        try FileManager.default.removeItem(at: original)

        #expect(copy.deletingLastPathComponent().standardizedFileURL == drops.standardizedFileURL)
        #expect(copy.lastPathComponent.hasSuffix("-Screenshot 1.png"))
        #expect(try Data(contentsOf: copy) == Data("png".utf8))
    }

    @Test func aFileThatCannotBeReadIsNotAttached() throws {
        let root = try scratch()
        defer { try? FileManager.default.removeItem(at: root) }
        #expect(DropStash.stash(root.appendingPathComponent("gone.png"), in: root.appendingPathComponent("drops")) == nil)
    }

    @Test func aFileAlreadyInTheDropsFolderIsItsOwnCopy() throws {
        let root = try scratch()
        defer { try? FileManager.default.removeItem(at: root) }
        let drops = root.appendingPathComponent("drops")
        try FileManager.default.createDirectory(at: drops, withIntermediateDirectories: true)
        let file = drops.appendingPathComponent("a.png")
        try Data("x".utf8).write(to: file)
        #expect(DropStash.stash(file, in: drops) == file)
    }

    @Test func removesCopiesOlderThanTwoWeeks() throws {
        let root = try scratch()
        defer { try? FileManager.default.removeItem(at: root) }
        let drops = root.appendingPathComponent("drops")
        try FileManager.default.createDirectory(at: drops, withIntermediateDirectories: true)
        let old = drops.appendingPathComponent("old.png")
        try Data("x".utf8).write(to: old)
        let fresh = root.appendingPathComponent("new.png")
        try Data("y".utf8).write(to: fresh)

        _ = DropStash.stash(fresh, in: drops, now: Date().addingTimeInterval(DropStash.keepFor + 60))
        #expect(!FileManager.default.fileExists(atPath: old.path))
    }
}

import Foundation

/// Files dropped on the composer are copied into `<tracks home>/drops` at the moment of the drop.
///
/// A screenshot dragged from the corner thumbnail is a file in `/var/folders/.../TemporaryItems/NSIRD_screencaptureui_*`:
/// macOS removes it when the thumbnail goes, and TCC keeps other processes (the `prism server` launchd job, the desk's
/// agent) from reading it. The copy is Emil's own file in a folder every one of them can read, and its path is what the
/// message carries.
public enum DropStash {
    /// Copies older than this are removed the next time something is dropped.
    static let keepFor: TimeInterval = 14 * 24 * 3600

    public static func directory(environment: [String: String] = ProcessInfo.processInfo.environment) -> URL {
        ServeConfig.directory(environment: environment).appendingPathComponent("drops", isDirectory: true)
    }

    /// The copy of `url` in the drops folder, or nil when it can't be read or copied.
    public static func stash(_ url: URL, in dir: URL = directory(), now: Date = Date()) -> URL? {
        let fm = FileManager.default
        guard url.isFileURL else { return nil }
        // A file already in the drops folder is its own copy.
        if url.standardizedFileURL.deletingLastPathComponent().path == dir.standardizedFileURL.path { return url }
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
            prune(dir, now: now)
            let name = url.lastPathComponent.isEmpty ? "drop" : url.lastPathComponent
            let target = dir.appendingPathComponent("\(UUID().uuidString.prefix(8))-\(name)")
            try fm.copyItem(at: url, to: target)
            return target
        } catch {
            return nil
        }
    }

    static func prune(_ dir: URL, now: Date) {
        let fm = FileManager.default
        let files = (try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.contentModificationDateKey])) ?? []
        for file in files {
            let modified = (try? file.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
            if let modified, now.timeIntervalSince(modified) > keepFor { try? fm.removeItem(at: file) }
        }
    }
}

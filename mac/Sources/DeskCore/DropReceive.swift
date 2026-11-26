import AppKit
import Foundation

extension DropStash {
    /// Stashes what a drag pasteboard carries and calls `completion` on the main queue with the copies' URLs.
    ///
    /// The corner screenshot thumbnail offers a file promise (and a TemporaryItems file URL, and image data). A promise is
    /// the real source: the sender writes the file when asked, into a folder of ours. Only without promises do file URLs
    /// count, and only without those the image data.
    ///
    /// Call it synchronously from `performDragOperation`: AppKit only lets a promise be received there. `receivePromise`
    /// is the seam for tests, which have no drag session; it defaults to the real receiver.
    public static func receive(from pasteboard: NSPasteboard, in dir: URL = directory(), now: Date = Date(),
                               receivePromise: @escaping (NSFilePromiseReceiver, URL, OperationQueue, @escaping (URL, Error?) -> Void) -> Void = {
                                   $0.receivePromisedFiles(atDestination: $1, options: [:], operationQueue: $2, reader: $3)
                               },
                               completion: @escaping ([URL]) -> Void) {
        let fm = FileManager.default
        let promises = (pasteboard.readObjects(forClasses: [NSFilePromiseReceiver.self]) as? [NSFilePromiseReceiver]) ?? []
        if !promises.isEmpty {
            let incoming = dir.appendingPathComponent(".incoming-\(UUID().uuidString.prefix(8))", isDirectory: true)
            try? fm.createDirectory(at: incoming, withIntermediateDirectories: true)
            prune(dir, now: now)
            let queue = OperationQueue()
            let lock = NSLock()
            nonisolated(unsafe) var done: [URL] = []
            let group = DispatchGroup()
            for promise in promises {
                group.enter()
                receivePromise(promise, incoming, queue) { file, error in
                    if error == nil, let copy = stash(file, in: dir, now: now) {
                        lock.lock(); done.append(copy); lock.unlock()
                    }
                    group.leave()
                }
            }
            group.notify(queue: .main) {
                try? fm.removeItem(at: incoming)
                completion(done)
            }
            return
        }
        let urls = ((pasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL]) ?? [])
            .compactMap { stash($0, in: dir, now: now) }
        if !urls.isEmpty { return completion(urls) }
        if let data = pasteboard.data(forType: .png) ?? pasteboard.data(forType: .tiff),
           let png = pngData(data), let copy = stash(data: png, ext: "png", in: dir, now: now) {
            return completion([copy])
        }
        completion([])
    }

    /// `data` written into the drops folder as `<id>-image.<ext>`.
    public static func stash(data: Data, ext: String, in dir: URL = directory(), now: Date = Date()) -> URL? {
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            prune(dir, now: now)
            let target = dir.appendingPathComponent("\(UUID().uuidString.prefix(8))-image.\(ext)")
            try data.write(to: target)
            return target
        } catch {
            return nil
        }
    }

    static func pngData(_ data: Data) -> Data? {
        if data.starts(with: [0x89, 0x50, 0x4E, 0x47]) { return data }
        guard let rep = NSBitmapImageRep(data: data) else { return nil }
        return rep.representation(using: .png, properties: [:])
    }
}

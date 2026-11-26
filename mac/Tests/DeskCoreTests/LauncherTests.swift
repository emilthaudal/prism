import Foundation
import Testing
@testable import DeskCore

/// A tracks that runs `body` when called as `tracks serve`, with a TRACKS_HOME of its own and a listener it can point serve.json at.
private struct FakeTracks {
    let home: URL
    let binary: URL
    let listener: Int32
    let port: Int

    init(_ body: String) throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        home = dir.appendingPathComponent("home")
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        (listener, port) = Self.listen()
        binary = dir.appendingPathComponent("tracks")
        let script = "#!/bin/sh\n[ \"$1\" = serve ] || exit 64\n" + body
            .replacingOccurrences(of: "INFO", with: "printf '{\"url\":\"http://127.0.0.1:\(port)\",\"token\":\"t\",\"pid\":%s}' $$ > \"\(home.path)/serve.json\"")
        try script.write(to: binary, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: binary.path)
    }

    private static func listen() -> (Int32, Int) {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        _ = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
        Darwin.listen(fd, 8)
        var bound = sockaddr_in()
        var len = socklen_t(MemoryLayout<sockaddr_in>.size)
        _ = withUnsafeMutablePointer(to: &bound) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &len) } }
        return (fd, Int(UInt16(bigEndian: bound.sin_port)))
    }

    func launcher(wait: TimeInterval = 10) -> ServeLauncher { ServeLauncher(binary: binary, home: home, wait: wait, pollInterval: 0.05) }
    func close() { Darwin.close(listener) }
}

@Suite struct LauncherTests {
    @Test func startsServeAndFindsItsServeJSON() async throws {
        let fake = try FakeTracks("INFO\necho started\nexec sleep 3\n")
        defer { fake.close() }
        try await fake.launcher().ensureRunning()
        #expect(fake.launcher().reachable())
        let log = try String(contentsOf: fake.home.appendingPathComponent("serve.log"), encoding: .utf8)
        #expect(log.contains("started"))
    }

    @Test func waitsForASlowStart() async throws {
        let fake = try FakeTracks("sleep 1.5\nINFO\nexec sleep 3\n")
        defer { fake.close() }
        let start = Date()
        try await fake.launcher().ensureRunning()
        #expect(Date().timeIntervalSince(start) >= 1.4)
    }

    @Test func showsTheRefusalInsteadOfWaiting() async throws {
        let fake = try FakeTracks("echo 'tracks: the desk runs in Rex session abc' >&2\nexit 1\n")
        defer { fake.close() }
        let start = Date()
        await #expect(throws: ClientError.self) { try await fake.launcher().ensureRunning() }
        #expect(Date().timeIntervalSince(start) < 5)
        do { try await fake.launcher().ensureRunning() } catch {
            #expect(error.localizedDescription.contains("the desk runs in Rex session abc"))
        }
    }

    @Test func leavesAReachableServeAlone() async throws {
        let fake = try FakeTracks("echo second > \"\(NSTemporaryDirectory())never-\(UUID().uuidString)\"\nexit 1\n")
        defer { fake.close() }
        try Data(#"{"url": "http://127.0.0.1:\#(fake.port)", "token": "t", "pid": \#(getpid())}"#.utf8).write(to: fake.home.appendingPathComponent("serve.json"))
        try await fake.launcher().ensureRunning()
        #expect(!FileManager.default.fileExists(atPath: fake.home.appendingPathComponent("serve.log").path))
    }

    @Test func startsOneWhenServeJSONIsStale() async throws {
        let fake = try FakeTracks("INFO\nexec sleep 3\n")
        defer { fake.close() }
        try Data(#"{"url": "http://127.0.0.1:1", "token": "old", "pid": 1}"#.utf8).write(to: fake.home.appendingPathComponent("serve.json"))
        try await fake.launcher().ensureRunning()
        #expect(try ServeConfig.load(from: fake.home.appendingPathComponent("serve.json")).token == "t")
    }

    @Test @MainActor func aFailedStartIsShownAndNotRetriedInALoop() async throws {
        let store = DeskStore(client: ScriptedClient(snapshots: [], batches: []))
        let attempts = Counter()
        store.prepare = { attempts.bump(); throw ClientError.startFailed("refused", log: "x") }
        await store.run()
        #expect(store.connection == .startFailed("Prism server did not start: refused (log: x)"))
        #expect(attempts.value == 1)
    }

    @Test @MainActor func aLostServeIsStartedAgainBeforeReconnecting() async throws {
        let client = ScriptedClient(snapshots: [Snapshot(seq: 1)], batches: [[]])
        let sleeps = SleepLog()
        let store = DeskStore(client: client, sleep: { d in
            if await sleeps.record(d) >= 2 { throw CancellationError() }
        })
        let attempts = Counter()
        store.prepare = { attempts.bump() }
        await store.run()
        #expect(await client.snapshotFetches == 2)
        #expect(attempts.value == 2)
    }
}

final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0
    func bump() { lock.withLock { n += 1 } }
    var value: Int { lock.withLock { n } }
}

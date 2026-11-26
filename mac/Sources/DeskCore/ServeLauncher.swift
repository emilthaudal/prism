import Darwin
import Foundation

/// Starts `prism server` when none is reachable, so opening the app is enough.
public struct ServeLauncher: Sendable {
    public var binary: URL
    public var home: URL
    public var wait: TimeInterval
    public var pollInterval: TimeInterval

    /// What a second serve prints when the first holds serve.lock: the client then connects to the first.
    static let loserMessage = "runs already"

    public init(binary: URL, home: URL, wait: TimeInterval = 10, pollInterval: TimeInterval = 0.1) {
        self.binary = binary
        self.home = home
        self.wait = wait
        self.pollInterval = pollInterval
    }

    public init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        let path = environment["TRACKS_BIN"].flatMap { $0.isEmpty ? nil : $0 }
            ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".local/bin/tracks").path
        self.init(binary: URL(fileURLWithPath: path), home: ServeConfig.directory(environment: environment))
    }

    public var logFile: URL { home.appendingPathComponent("serve.log") }

    /// Whether serve.json names a live serve that accepts connections.
    public func reachable() -> Bool {
        guard let config = try? ServeConfig.load(from: home.appendingPathComponent("serve.json")) else { return false }
        if let pid = config.pid, pid > 0, kill(pid_t(pid), 0) != 0, errno != EPERM { return false }
        guard let host = config.url.host, let port = config.url.port else { return false }
        return Self.canConnect(host: host, port: port)
    }

    private static func canConnect(host: String, port: Int) -> Bool {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = in_port_t(port).bigEndian
        guard inet_pton(AF_INET, host == "localhost" ? "127.0.0.1" : host, &addr.sin_addr) == 1 else { return false }
        return withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) == 0 }
        }
    }

    /// Returns once a serve is reachable: the one already running, or the one it starts. Throws serve's own message when
    /// serve exits with an error.
    public func ensureRunning() async throws {
        if reachable() { return }
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        let offset = (try? FileManager.default.attributesOfItem(atPath: logFile.path)[.size] as? Int) ?? 0
        var pid = try spawn()
        let deadline = Date().addingTimeInterval(wait)
        while true {
            if reachable() { return }
            if pid != 0, let status = exitStatus(pid) {
                pid = 0
                let message = logText(after: offset)
                if status != 0, !message.contains(Self.loserMessage) {
                    throw ClientError.startFailed(message.isEmpty ? "it exited without a message" : message, log: logFile.path)
                }
            }
            if let refusal = Self.refusal(in: logText(after: offset)) {
                throw ClientError.startFailed(refusal, log: logFile.path)
            }
            if Date() >= deadline { throw ClientError.startTimedOut(seconds: Int(wait), log: logFile.path) }
            try await Task.sleep(for: .seconds(pollInterval))
        }
    }

    /// The error a detached serve exited with, as tracks prints it ("tracks: …"); losing serve.lock to another serve is none.
    static func refusal(in log: String) -> String? {
        log.split(separator: "\n").lazy.map(String.init)
            .first { $0.hasPrefix("tracks: ") && !$0.contains(loserMessage) }
            .map { String($0.dropFirst("tracks: ".count)) }
    }

    /// Runs `tracks serve --detach` (prism server under its old name, so an older tracks still starts it), which starts serve as a launchd job of its own and returns. A process the app spawns
    /// itself dies with the app's launchd job when the app quits, setsid or not, and takes the desk's background sessions
    /// with it. Its output is appended to serve.log, where serve writes too.
    private func spawn() throws -> pid_t {
        var actions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&actions)
        defer { posix_spawn_file_actions_destroy(&actions) }
        posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
        posix_spawn_file_actions_addopen(&actions, 1, logFile.path, O_WRONLY | O_APPEND | O_CREAT, 0o600)
        posix_spawn_file_actions_adddup2(&actions, 1, 2)
        var attrs: posix_spawnattr_t?
        posix_spawnattr_init(&attrs)
        defer { posix_spawnattr_destroy(&attrs) }
        posix_spawnattr_setflags(&attrs, Int16(POSIX_SPAWN_SETSID))
        let args = [binary.path, "serve", "--detach"]
        let argv = args.map { strdup($0) } + [nil]
        defer { argv.forEach { free($0) } }
        var pid: pid_t = 0
        let rc = posix_spawn(&pid, binary.path, &actions, &attrs, argv, environ)
        guard rc == 0 else {
            throw ClientError.startFailed("can't run \(binary.path): \(String(cString: strerror(rc)))", log: logFile.path)
        }
        return pid
    }

    /// The exit status once pid has exited, else nil; a pid that can't be waited on counts as a failure.
    private func exitStatus(_ pid: pid_t) -> Int32? {
        var status: Int32 = 0
        let rc = waitpid(pid, &status, WNOHANG)
        if rc == 0 { return nil }
        if rc < 0 { return -1 }
        return (status & 0x7f) == 0 ? (status >> 8) & 0xff : -1
    }

    private func logText(after offset: Int) -> String {
        guard let data = try? Data(contentsOf: logFile), data.count > offset else { return "" }
        return String(decoding: data[offset...], as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

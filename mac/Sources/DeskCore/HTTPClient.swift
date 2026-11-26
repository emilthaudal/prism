import Foundation

/// The live client. It reads serve.json on every request, so a restarted server on a new port is found again.
public struct HTTPClient: DeskClient {
    public let serveFile: URL
    private let session: URLSession

    /// The server sends a comment every 15 s, so a stream quiet for this long is dead.
    private static let streamIdleTimeout: TimeInterval = 45
    private static let requestTimeout: TimeInterval = 15

    public init(serveFile: URL = ServeConfig.file()) {
        self.serveFile = serveFile
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = Self.streamIdleTimeout
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        session = URLSession(configuration: config)
    }

    private func request(_ path: String, method: String = "GET", body: [String: any Sendable]? = nil, timeout: TimeInterval = requestTimeout) throws -> URLRequest {
        let config = try ServeConfig.load(from: serveFile)
        var req = URLRequest(url: URL(string: path, relativeTo: config.url.appendingPathComponent(""))!.absoluteURL)
        req.httpMethod = method
        req.timeoutInterval = timeout
        req.setValue("Bearer \(config.token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        return req
    }

    private func send(_ req: URLRequest) async throws -> Data {
        let (data, response) = try await session.data(for: req)
        try Self.check(response, data: data)
        return data
    }

    private static func check(_ response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            let doc = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
            let message = doc?["error"] as? String ?? ""
            if http.statusCode == 409, let focus = doc?["focus"] as? [String] {
                throw ClientError.focusFull(message: message, focus: focus)
            }
            throw ClientError.http(status: http.statusCode, message: message)
        }
    }

    public func snapshot() async throws -> Snapshot {
        try DeskJSON.decoder().decode(Snapshot.self, from: try await send(try request("v1/snapshot")))
    }

    public func transcript(session id: String) async throws -> Transcript {
        let escaped = id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? id
        return try DeskJSON.decoder().decode(Transcript.self, from: try await send(try request("v1/sessions/\(escaped)/transcript")))
    }

    public func perform(_ action: DeskAction) async throws -> ActionReply {
        let (path, body) = action.request
        let data = try await send(try request(path, method: "POST", body: body))
        return (try? DeskJSON.decoder().decode(ActionReply.self, from: data)) ?? ActionReply()
    }

    public func events(since seq: Int) -> AsyncThrowingStream<StreamEvent, any Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var req = try request("v1/events?since=\(seq)", timeout: Self.streamIdleTimeout)
                    req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    let (bytes, response) = try await session.bytes(for: req)
                    try Self.check(response, data: Data())
                    var lines = LineSplitter()
                    var parser = SSEParser()
                    let decoder = DeskJSON.decoder()
                    for try await byte in bytes {
                        guard let line = lines.feed(byte), let message = parser.feed(line: line) else { continue }
                        continuation.yield(try decoder.decode(StreamEvent.self, from: Data(message.data.utf8)))
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}

import Foundation

/// A session's activity ("TOOL: what", from the session hook) as a person would say it: "swift test" rather than
/// "Bash: cd mac && timeout 580 swift test > $CLAUDE_JOB_DIR/tmp/swift.log 2>&1; gr…".
public enum Activity {
    public static func readable(_ raw: String) -> String {
        let line = raw.trimmingCharacters(in: .whitespaces)
        guard let colon = line.firstIndex(of: ":") else { return line }
        let tool = String(line[..<colon])
        let what = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        guard !what.isEmpty, !tool.contains(" ") else { return line }
        switch tool {
        case "Bash": return command(what).map { "Running \($0)" } ?? "Running a command"
        case "Read": return "Reading \(what)"
        case "Write": return "Writing \(what)"
        case "Edit", "MultiEdit", "NotebookEdit": return "Editing \(what)"
        case "Grep", "Glob": return "Searching the code"
        case "WebFetch": return "Reading \(host(what) ?? "a page")"
        case "WebSearch": return "Searching the web"
        case "Agent", "Task": return what
        default: return line
        }
    }

    /// The program a shell line runs, with its subcommand: the first part that isn't `cd`, an assignment or a
    /// wrapper such as `timeout 580`.
    static func command(_ shell: String) -> String? {
        let parts = shell.replacingOccurrences(of: "&&", with: ";").replacingOccurrences(of: "||", with: ";")
            .split(separator: ";")
        for part in parts {
            var words = part.split(separator: " ").map(String.init)
            // An assignment's value can hold spaces ("U=$(python3 -c …)"): such a part names no program.
            if let w = words.first, skip(w) { continue }
            while let w = words.first, words.count > 1 && wrapper(w) {
                words.removeFirst()
                if wrapper(w), let n = words.first, Int(n) != nil || n.hasPrefix("-") { words.removeFirst() }
            }
            guard let program = words.first.map(base), plain(program) else { continue }
            if ["cd", "export", "set", "echo", "sleep", "true", "pushd", "popd"].contains(program) { continue }
            if words.count > 1, plain(words[1]), !words[1].hasPrefix("-") { return "\(program) \(words[1])" }
            return program
        }
        return nil
    }

    private static func skip(_ w: String) -> Bool { w.contains("=") && !w.hasPrefix("-") }
    private static func wrapper(_ w: String) -> Bool { ["timeout", "env", "nice", "time", "command", "exec", "sudo"].contains(w) }
    private static func base(_ w: String) -> String { w.split(separator: "/").last.map(String.init) ?? w }
    private static func plain(_ w: String) -> Bool {
        (w.first?.isLetter ?? false) && w.count <= 24 && w.allSatisfy { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" || $0 == "." }
    }
    private static func host(_ url: String) -> String? { URL(string: url)?.host() }
}

import Foundation

/// The desk names a question by its D id in chat and never numbers one there; Prism shows each id
/// it names as a live chip under the message, so Emil can answer where he reads.
public enum DecisionRefs {
    /// The D ids `text` names, each once, in the order they first appear. "D41" counts; "AD41", "D41a" and "D0" don't.
    public static func ids(in text: String) -> [String] {
        var out: [String] = []
        let chars = Array(text)
        var i = 0
        while i < chars.count {
            let atWordStart = i == 0 || !(chars[i - 1].isLetter || chars[i - 1].isNumber)
            guard chars[i] == "D", atWordStart else { i += 1; continue }
            var j = i + 1
            while j < chars.count, chars[j].isASCII, chars[j].isNumber { j += 1 }
            let atWordEnd = j == chars.count || !(chars[j].isLetter || chars[j].isNumber)
            if j > i + 1, atWordEnd, chars[i + 1] != "0" {
                let id = String(chars[i..<j])
                if !out.contains(id) { out.append(id) }
            }
            i = j
        }
        return out
    }

    /// The questions `text` names that the snapshot holds, in the order named.
    public static func questions(in text: String, from snapshot: Snapshot) -> [Question] {
        ids(in: text).compactMap { snapshot.question($0) }
    }
}

extension Question {
    /// One line for a chip: the answer when answered, else the options with their keys.
    public var chipSummary: String {
        if let answer {
            if let n = answer.option, options.indices.contains(n) { return "answered: \(options[n].label)" }
            if let t = answer.text, !t.isEmpty { return "answered: \(t)" }
            return "closed"
        }
        if options.isEmpty { return recommendationText.map { "recommend: \($0)" } ?? "open" }
        return options.enumerated().map { i, o in "\(i + 1) \(o.label)\(i == recommendedIndex ? " ★" : "")" }.joined(separator: " · ")
    }
}

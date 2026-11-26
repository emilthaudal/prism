import Testing
@testable import DeskCore

@Suite struct ActivityTests {
    @Test func aShellLineReadsAsTheProgramItRuns() {
        #expect(Activity.readable("Bash: cd mac && timeout 580 swift test > $CLAUDE_JOB_DIR/tmp/swift.log 2>&1; gr…") == "Running swift test")
        #expect(Activity.readable(#"Bash: S=${TRACKS_HOME:-$HOME/.local/state/tracks}/serve.json; U=$(python3 -c "i…"#) == "Running a command")
        #expect(Activity.readable("Bash: bun test --shard 2/3") == "Running bun test")
        #expect(Activity.readable("Bash: curl -s -H x http://a") == "Running curl")
        #expect(Activity.readable("Bash: FOO=1 make install") == "Running a command")
        #expect(Activity.readable("Bash: /usr/bin/git status") == "Running git status")
    }

    @Test func otherToolsReadAsWhatTheyDo() {
        #expect(Activity.readable("Edit: refunds.go") == "Editing refunds.go")
        #expect(Activity.readable("Read: Board.swift") == "Reading Board.swift")
        #expect(Activity.readable("Grep: func title") == "Searching the code")
        #expect(Activity.readable("WebFetch: https://docs.example.com/x") == "Reading docs.example.com")
        #expect(Activity.readable("Agent: review the diff") == "review the diff")
        #expect(Activity.readable("Bash") == "Bash")
        #expect(Activity.readable("mcp__linear__save_issue: x") == "mcp__linear__save_issue: x")
    }
}

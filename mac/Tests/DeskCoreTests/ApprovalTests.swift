import Foundation
import Testing
@testable import DeskCore

@Suite struct ApprovalTests {
    @Test func onlyWebLinksOpen() {
        let q = Question(id: "D3", question: "gatekeeper: Approve merging #42", links: ["https://gatekeeper.example/confirm/abc", "EMI-422", "file:///etc/passwd", " http://localhost:8080/x "])
        #expect(q.openableLinks.map(\.absoluteString) == ["https://gatekeeper.example/confirm/abc", "http://localhost:8080/x"])
        #expect(Question(id: "D4", question: "Merge?").openableLinks.isEmpty)
    }

    @Test func anMCPApprovalIsAPromptTheSessionWaitsOn() {
        #expect(Session(id: "s1", status: "blocked", waitingFor: "approval in gatekeeper: Approve merging #42").waitsOnPermission)
        #expect(Session(id: "s2", status: "blocked", waitingFor: "permission: Bash gh pr create").waitsOnPermission)
        #expect(!Session(id: "s3", status: "blocked", waitingFor: "Claude Code needs your input").waitsOnPermission)
        #expect(!Session(id: "s4", status: "working", waitingFor: "approval in gatekeeper: x").waitsOnPermission)
    }
}

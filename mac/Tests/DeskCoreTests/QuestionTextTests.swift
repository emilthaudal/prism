import Testing
@testable import DeskCore

/// A made-up ask in the shape that came in as one bold wall: a first sentence, then steps written inline.
let longAsk = "Staging run of the pantry refund screen (#418), about 3 minutes. (a) Run `! aws sso login --profile pantry-staging` here, so I can send two test orders to the staging shop. Answer \"go\" and I seed them. (b) Once I say it's on staging: open https://staging.pantry.example signed in with the demo account. (c) Click \"Refunds\" in the nav: two orders from test-buyer@example.com should be there. (d) On the first one, press Refund and tell me what the total says."

@Suite struct QuestionTextTests {
    @Test func aShortQuestionIsOnlyATitle() {
        let t = QuestionText("Merge PR #418 in pantry-api now?")
        #expect(t == QuestionText(title: "Merge PR #418 in pantry-api now?"))
        #expect(!t.folds)
    }

    @Test func aLongAskSplitsIntoATitleAndItsSteps() {
        let t = QuestionText(longAsk)
        #expect(t.title == "Staging run of the pantry refund screen (#418), about 3 minutes.")
        #expect(t.body.count == 4)
        #expect(t.body[0] == .step("a", "Run `! aws sso login --profile pantry-staging` here, so I can send two test orders to the staging shop. Answer \"go\" and I seed them."))
        #expect(t.body.map { if case .step(let m, _) = $0 { m } else { "" } } == ["a", "b", "c", "d"])
        #expect(t.folds)
    }

    @Test func textBeforeTheFirstStepIsItsOwnParagraph() {
        let t = QuestionText("Which way do we roll out the new pricing to every shop in the group this week? Context first: the old rules stay on until Friday. 1) flag per shop 2) all at once")
        #expect(t.title == "Which way do we roll out the new pricing to every shop in the group this week?")
        #expect(t.body == [.text("Context first: the old rules stay on until Friday."), .step("1", "flag per shop"), .step("2", "all at once")])
    }

    @Test func markersOutOfOrderOrInCodeStayInTheText() {
        #expect(QuestionText.parts("Only (b) here and (c) there") == [.text("Only (b) here and (c) there")])
        #expect(QuestionText.parts("Run `x (a) y (b) z` now") == [.text("Run `x (a) y (b) z` now")])
        #expect(QuestionText.parts("One step (a) only") == [.text("One step (a) only")])
    }

    @Test func aSentenceEndInsideCodeIsNotTheTitle() {
        let q = "Run `make test. then ship` before the release goes out to every shop in the group, and tell me what it printed when it ends: it takes a while."
        let t = QuestionText(q)
        #expect(t.title == "Run `make test. then ship` before the release goes out to every shop in the group, and tell me what it printed when it ends:")
    }

    @Test func noEarlySentenceEndLeavesNoTitle() {
        let q = String(repeating: "word ", count: 40) + "end."
        let t = QuestionText(q)
        #expect(t.title.isEmpty)
        #expect(t.headline == q)
    }

    @Test func aFirstLineIsTheTitleOfAQuestionWrittenInLines() {
        let t = QuestionText("Ship it?\nCI is green.\nAlex approved.")
        #expect(t == QuestionText(title: "Ship it?", body: [.text("CI is green. Alex approved.")]))
    }
}

import Foundation
import Testing
@testable import DeskCore

@Suite struct DemoScriptTests {
    private let sample = fixtureURL.deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("mac/scripts/motion-sample.json")

    @Test func theSampleScriptReadsAndPlaysOnTheFixture() async throws {
        let script = try DemoScript.load(sample)
        #expect(script.steps.count >= 7)
        let client = FixtureClient(snapshot: try loadFixture())
        for step in script.steps {
            switch step.action {
            case .event(let change): await client.inject(change)
            case .mode(let track, let mode): _ = try await client.perform(.mode(track: track, mode: mode, swap: nil))
            default: break
            }
        }
        let s = await client.snapshot()
        #expect(s.conversation.last?.role == "report")
        #expect(s.question("D16")?.isOpen == true)
        #expect(s.track("119")?.mode == "waiting")
    }

    @Test func theUISampleScriptReadsAndNamesEachStillOnce() throws {
        let url = sample.deletingLastPathComponent().appendingPathComponent("ui-sample.json")
        let script = try DemoScript.load(url)
        let shots = script.steps.compactMap(\.shot)
        #expect(shots.count >= 8)
        #expect(Set(shots).count == shots.count)
        #expect(script.steps.contains { $0.action == .hover("row:track:114") })
        #expect(script.steps.contains { $0.action == .press("card:D14") })
        #expect(script.steps.contains { $0.action == .focus("desk") })
    }

    @Test func aStepReadsTheStateActions() throws {
        let json = """
        {"steps": [
          {"name": "a", "at": 0, "do": "hover", "target": "card:D14", "shot": "card-hover"},
          {"name": "b", "at": 1, "do": "press", "target": "row:desk"},
          {"name": "c", "at": 2, "do": "press"},
          {"name": "d", "at": 3, "do": "focus", "field": "answer"},
          {"name": "e", "at": 4, "do": "select", "item": "session:abc"},
          {"name": "f", "at": 5, "do": "dismiss"},
          {"name": "g", "at": 6, "do": "draft", "text": "hi"}
        ]}
        """
        let script = try DeskJSON.decoder().decode(DemoScript.self, from: Data(json.utf8))
        #expect(script.steps.map(\.action) == [.hover("card:D14"), .press("row:desk"), .press(""), .focus("answer"), .select("session:abc"), .dismiss, .draft("hi")])
        #expect(script.steps[0].shot == "card-hover")
        #expect(script.steps[1].shot == nil)
    }

    @Test func aStepReadsEachAction() throws {
        let json = """
        {"lead": 0.5, "steps": [
          {"name": "a", "at": 0, "do": "pick", "question": "D14"},
          {"name": "b", "at": 1, "do": "answer", "question": "D14", "option": 2, "expect": 0.2},
          {"name": "c", "at": 2, "do": "fold", "fold": "handled", "crossfade": true, "again": 0.3},
          {"name": "d", "at": 3, "do": "wait", "continuous": true},
          {"name": "e", "at": 4, "do": "event", "event": {"op": "remove", "kind": "question", "value": {"id": "D14"}}},
          {"name": "f", "at": 5, "do": "undo", "question": "D14"},
          {"name": "g", "at": 6, "do": "undo"}
        ]}
        """
        let script = try DeskJSON.decoder().decode(DemoScript.self, from: Data(json.utf8))
        #expect(script.lead == 0.5)
        #expect(script.tail == 1.5)
        #expect(script.steps.map(\.action) == [.pick("D14"), .answer("D14", option: 2), .fold("handled"), .wait, .event(.remove(kind: "question", id: "D14")), .undo("D14"), .undo(nil)])
        #expect(script.steps[1].expect == 0.2)
        #expect(script.steps[2].crossfade)
        #expect(script.steps[2].again == 0.3)
        #expect(script.steps[3].continuous)
    }

    @Test func theArrivalScriptReadsAndItsReportsLandInOneTurn() async throws {
        let url = fixtureURL.deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("mac/scripts/motion-arrive.json")
        let script = try DemoScript.load(url)
        let names = script.steps.map(\.name)
        for scenario in ["report arrives", "two reports in one snapshot", "report arrives while scrolled up", "decision answered on the board", "session lands"] {
            #expect(names.contains(scenario))
        }
        let long = fixtureURL.deletingLastPathComponent().appendingPathComponent("serve-fixture-long.json")
        let client = FixtureClient(snapshot: try DeskJSON.decoder().decode(Snapshot.self, from: Data(contentsOf: long)))
        let before = await client.snapshot().conversation.count
        for step in script.steps {
            switch step.action {
            case .event(let change): await client.inject(change)
            case .events(let changes): for change in changes { await client.inject(change) }
            default: break
            }
        }
        #expect(await client.snapshot().conversation.count == before + 4)
        #expect(script.steps.contains { if case .scroll("up") = $0.action { true } else { false } })
    }

    @Test func stepsOutOfOrderOrUnknownAreRefused() {
        let late = #"{"steps": [{"name": "a", "at": 2, "do": "wait"}, {"name": "b", "at": 1, "do": "wait"}]}"#
        let unknown = #"{"steps": [{"name": "a", "at": 0, "do": "dance"}]}"#
        #expect(throws: DecodingError.self) { try DeskJSON.decoder().decode(DemoScript.self, from: Data(late.utf8)) }
        #expect(throws: DecodingError.self) { try DeskJSON.decoder().decode(DemoScript.self, from: Data(unknown.utf8)) }
    }
}

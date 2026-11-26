import Foundation
import Testing
@testable import DeskCore

@Suite struct AttachmentTests {
    private let json = """
    {"id": "D20", "question": "Which icon?", "recommended": 0,
     "options": [
       {"label": "owl looking back", "description": "", "attachment": {"label": "owl.png", "url": "file:///tmp/owl.png", "kind": "image"}},
       {"label": "mended parcel", "attachment": {"label": "parcel.png", "url": "file:///tmp/parcel.png", "kind": "image"}}],
     "attachments": [{"label": "sheet.html", "url": "https://example.com/sheet.html", "kind": "page"}],
     "created_at": "2026-11-25T07:00:00Z", "answer": null}
    """

    @Test func decodesOptionsWithPicturesAndQuestionAttachments() throws {
        let q = try DeskJSON.decoder().decode(Question.self, from: Data(json.utf8))
        #expect(q.options[0].attachment == Attachment(label: "owl.png", url: "file:///tmp/owl.png", kind: "image"))
        #expect(q.options[1].attachment?.label == "parcel.png")
        #expect(q.attachments?.first?.attachmentKind == .page)
        #expect(q.allAttachments.map(\.label) == ["sheet.html", "owl.png", "parcel.png"])
        #expect(q.isVisualChoice)
    }

    @Test func aQuestionWithoutAttachmentsStillDecodes() throws {
        let q = try DeskJSON.decoder().decode(Question.self, from: Data(#"{"id": "D1", "question": "Go?", "options": [{"label": "Yes"}]}"#.utf8))
        #expect(q.attachments == nil)
        #expect(q.options[0].attachment == nil)
        #expect(q.allAttachments.isEmpty)
        #expect(!q.isVisualChoice)
    }

    @Test func aVisualChoiceNeedsAPictureOnEveryOption() {
        let pic = Attachment(label: "a.png", url: "file:///tmp/a.png", kind: "image")
        let page = Attachment(label: "p", url: "https://x.io", kind: "page")
        #expect(Question(id: "D1", question: "?", options: [Option(label: "a", attachment: pic), Option(label: "b", attachment: pic)]).isVisualChoice)
        #expect(!Question(id: "D1", question: "?", options: [Option(label: "a", attachment: pic), Option(label: "b")]).isVisualChoice)
        #expect(!Question(id: "D1", question: "?", options: [Option(label: "a", attachment: pic), Option(label: "b", attachment: page)]).isVisualChoice)
        #expect(!Question(id: "D1", question: "?", options: [Option(label: "a", attachment: pic)]).isVisualChoice)
    }

    @Test func localAndMissingFiles() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let here = dir.appendingPathComponent("here.png")
        try Data([0]).write(to: here)
        let present = Attachment(label: "here.png", url: here.absoluteString, kind: "image")
        let gone = Attachment(label: "gone.png", url: dir.appendingPathComponent("gone.png").absoluteString, kind: "image")
        let web = Attachment(label: "x.io", url: "https://x.io/a.png", kind: "image")
        #expect(present.isLocal && present.isAvailable && present.localPath == here.path)
        #expect(gone.isLocal && !gone.isAvailable)
        #expect(!web.isLocal && web.isAvailable && web.localPath == nil)
        let q = Question(id: "D1", question: "?", options: [Option(label: "a", attachment: present), Option(label: "b", attachment: gone)], attachments: [web])
        #expect(q.previewableImages == [present])
    }

    @Test func anUnknownKindReadsAsAFile() {
        #expect(Attachment(label: "x", url: "file:///tmp/x", kind: "video").attachmentKind == .file)
    }

    @Test func spaceOpensThePreviewOutsideTheComposer() {
        #expect(Keys.command(for: KeyPress(characters: " "), editing: false) == .preview)
        #expect(Keys.command(for: KeyPress(characters: " "), editing: true) == nil)
    }
}

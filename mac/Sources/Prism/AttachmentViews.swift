import AppKit
import DeskCore
import QuickLookUI
import SwiftUI

/// Quick Look for a question's pictures: one panel, paged through with the arrow keys.
@MainActor
final class QuickLook: NSObject, @preconcurrency QLPreviewPanelDataSource {
    static let shared = QuickLook()
    private var items: [NSURL] = []

    func show(_ images: [Attachment], at attachment: Attachment? = nil) {
        let urls = images.compactMap { $0.target.map { $0 as NSURL } }
        guard !urls.isEmpty, let panel = QLPreviewPanel.shared() else { return }
        items = urls
        panel.dataSource = self
        panel.reloadData()
        panel.currentPreviewItemIndex = attachment.flatMap { a in images.firstIndex(of: a) } ?? 0
        panel.makeKeyAndOrderFront(nil)
    }

    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int { items.count }
    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> (any QLPreviewItem)! { items[index] }
}

enum Attachments {
    /// Click on a picture: Quick Look for a local image, the browser or default app for anything else.
    @MainActor
    static func open(_ attachment: Attachment, among question: Question) {
        if attachment.attachmentKind == .image, attachment.isLocal, attachment.isAvailable {
            QuickLook.shared.show(question.previewableImages, at: attachment)
        } else if attachment.isAvailable, let url = attachment.target {
            NSWorkspace.shared.open(url)
        }
    }

    @MainActor
    static func preview(_ question: Question) {
        QuickLook.shared.show(question.previewableImages)
    }
}

/// A picture as a thumbnail; a file that is gone shows its path greyed, never an error.
struct AttachmentThumbnail: View {
    var attachment: Attachment
    var height: CGFloat
    var onOpen: () -> Void
    @State private var image: NSImage?
    @State private var loaded = false

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .scaledToFit()
                    .clipShape(RoundedRectangle(cornerRadius: Space.s, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Space.s, style: .continuous).strokeBorder(Theme.hairline))
                    .contentShape(Rectangle())
                    .onTapGesture(perform: onOpen)
                    .help("Quick Look \(attachment.label)")
            } else if loaded, let path = attachment.localPath, !attachment.isAvailable {
                MissingFile(path: path)
            } else if loaded {
                AttachmentButton(attachment: attachment, action: onOpen)
            } else {
                ProgressView().controlSize(.small).frame(width: height)
            }
        }
        .frame(maxHeight: height)
        .task(id: attachment.url) { await load() }
    }

    private func load() async {
        defer { loaded = true }
        guard let url = attachment.target, attachment.isAvailable else { return }
        let data: Data?
        if url.isFileURL {
            data = await Task.detached { try? Data(contentsOf: url) }.value
        } else {
            data = try? await URLSession.shared.data(from: url).0
        }
        image = data.flatMap { NSImage(data: $0) }
    }
}

struct MissingFile: View {
    var path: String

    var body: some View {
        Text((path as NSString).abbreviatingWithTildeInPath)
            .font(.prismMono)
            .foregroundStyle(.tertiary)
            .lineLimit(1)
            .truncationMode(.middle)
            .help("Not found: \(path)")
    }
}

/// A page or file: an Open button beside its name; a file that is gone shows its path greyed.
struct AttachmentButton: View {
    var attachment: Attachment
    var action: () -> Void

    var body: some View {
        if let path = attachment.localPath, !attachment.isAvailable {
            MissingFile(path: path)
        } else {
            HStack(spacing: Space.s) {
                Button("Open", action: action).controlSize(.small)
                Text(attachment.label).font(.prismMeta).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
        }
    }
}

/// The question's own attachments: pictures as thumbnails in a row, pages and files as Open buttons.
struct AttachmentStrip: View {
    var question: Question
    var thumbnailHeight: CGFloat

    var body: some View {
        let own = question.attachments ?? []
        if !own.isEmpty {
            let images = own.filter { $0.attachmentKind == .image }
            let others = own.filter { $0.attachmentKind != .image }
            VStack(alignment: .leading, spacing: Space.s) {
                if !images.isEmpty {
                    HStack(spacing: Space.s) {
                        ForEach(Array(images.enumerated()), id: \.offset) { _, a in
                            AttachmentThumbnail(attachment: a, height: thumbnailHeight) { Attachments.open(a, among: question) }
                        }
                    }
                }
                ForEach(Array(others.enumerated()), id: \.offset) { _, a in
                    AttachmentButton(attachment: a) { Attachments.open(a, among: question) }
                }
            }
        }
    }
}

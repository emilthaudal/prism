import AppKit
import DeskCore
import SwiftUI

/// Takes drops over the whole view it overlays, text field included (a text field swallows drops of its own), and
/// reads the dragging pasteboard directly: file promises, file URLs and image data, which SwiftUI's `URL` drop
/// destination misses for the corner screenshot thumbnail (EMI-399).
struct DropReceiver: NSViewRepresentable {
    var onDrop: ([URL]) -> Void

    func makeNSView(context: Context) -> DropView {
        let view = DropView()
        view.onDrop = onDrop
        return view
    }

    func updateNSView(_ view: DropView, context: Context) { view.onDrop = onDrop }

    final class DropView: NSView {
        var onDrop: ([URL]) -> Void = { _ in }

        override init(frame: NSRect) {
            super.init(frame: frame)
            registerForDraggedTypes(NSFilePromiseReceiver.readableDraggedTypes.map { NSPasteboard.PasteboardType($0) }
                + [.fileURL, .png, .tiff])
        }

        required init?(coder: NSCoder) { fatalError() }

        /// Clicks and typing reach the views below; only a drag in flight lands here.
        override func hitTest(_ point: NSPoint) -> NSView? {
            guard let type = NSApp.currentEvent?.type, type == .leftMouseDragged || type == .appKitDefined else { return nil }
            return super.hitTest(point)
        }

        override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation { .copy }

        override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
            // The promise has to be received inside this call; the copies arrive later.
            let drop = onDrop
            DropStash.receive(from: sender.draggingPasteboard) { urls in drop(urls) }
            return true
        }
    }
}

extension View {
    func dropReceiver(_ onDrop: @escaping ([URL]) -> Void) -> some View {
        overlay(DropReceiver(onDrop: onDrop))
    }
}

// Renders SVGs to square PNGs with WebKit, offscreen: no window on screen, no Screen Recording grant needed.
// WebKit handles the icon's blur and shadow filters, which NSImage's SVG support drops.
// Usage: swift scripts/render-svg.swift SVG PX OUT.png [SVG PX OUT.png ...]
import AppKit
import WebKit

@MainActor
final class Renderer: NSObject, WKNavigationDelegate {
    var jobs: [(svg: URL, px: Int, out: URL)]
    var view: WKWebView?
    var current: (svg: URL, px: Int, out: URL)?

    init(_ jobs: [(URL, Int, URL)]) { self.jobs = jobs.map { (svg: $0.0, px: $0.1, out: $0.2) } }

    func next() {
        guard !jobs.isEmpty else { exit(0) }
        let job = jobs.removeFirst()
        current = job
        // The page is laid out in points at 1x; the snapshot asks for exactly px pixels.
        let size = CGFloat(job.px)
        let web = WKWebView(frame: NSRect(x: 0, y: 0, width: size, height: size))
        web.setValue(false, forKey: "drawsBackground")
        web.navigationDelegate = self
        view = web
        guard let svg = try? String(contentsOf: job.svg, encoding: .utf8) else { fail("can't read \(job.svg.path)") }
        let html = "<html><body style=\"margin:0;background:transparent\"><div style=\"width:\(job.px)px;height:\(job.px)px\">"
            + svg.replacingOccurrences(of: "width=\"1024\" height=\"1024\"", with: "width=\"\(job.px)\" height=\"\(job.px)\"")
            + "</div></body></html>"
        web.loadHTMLString(html, baseURL: nil)
    }

    nonisolated func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        MainActor.assumeIsolated { snapshot(webView) }
    }

    func snapshot(_ web: WKWebView) {
        guard let job = current else { return }
        let config = WKSnapshotConfiguration()
        config.rect = NSRect(x: 0, y: 0, width: job.px, height: job.px)
        config.snapshotWidth = NSNumber(value: Double(job.px) / Double(NSScreen.main?.backingScaleFactor ?? 1))
        web.takeSnapshot(with: config) { image, error in
            guard let image else { self.fail("snapshot failed: \(String(describing: error))") }
            // Redraw into an exact px-by-px bitmap whatever the screen's scale.
            let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: job.px, pixelsHigh: job.px, bitsPerSample: 8,
                                       samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                                       bytesPerRow: 0, bitsPerPixel: 0)!
            rep.size = NSSize(width: job.px, height: job.px)
            NSGraphicsContext.saveGraphicsState()
            NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
            NSGraphicsContext.current?.imageInterpolation = .high
            image.draw(in: NSRect(x: 0, y: 0, width: job.px, height: job.px))
            NSGraphicsContext.restoreGraphicsState()
            do {
                try rep.representation(using: .png, properties: [:])!.write(to: job.out)
            } catch { self.fail("can't write \(job.out.path): \(error)") }
            self.next()
        }
    }

    func fail(_ why: String) -> Never {
        FileHandle.standardError.write(Data("render-svg: \(why)\n".utf8))
        exit(1)
    }
}

let args = Array(CommandLine.arguments.dropFirst())
guard !args.isEmpty, args.count % 3 == 0 else {
    FileHandle.standardError.write(Data("usage: render-svg SVG PX OUT.png [SVG PX OUT.png ...]\n".utf8))
    exit(2)
}
var jobs: [(URL, Int, URL)] = []
for i in stride(from: 0, to: args.count, by: 3) {
    guard let px = Int(args[i + 1]), px > 0 else { FileHandle.standardError.write(Data("bad size \(args[i + 1])\n".utf8)); exit(2) }
    jobs.append((URL(fileURLWithPath: args[i]), px, URL(fileURLWithPath: args[i + 2])))
}
let app = NSApplication.shared
app.setActivationPolicy(.prohibited)
let renderer = MainActor.assumeIsolated { Renderer(jobs) }
MainActor.assumeIsolated { renderer.next() }
app.run()

import AppKit
import DeskCore
import SwiftUI

/// The one spacing scale. Every margin, inset and gap in the window is one of these.
enum Space {
    /// Stacked flush: no gap.
    static let none: CGFloat = 0
    /// Only between stacked lines of text.
    static let xxs: CGFloat = 2
    static let xs: CGFloat = 4
    static let s: CGFloat = 8
    static let m: CGFloat = 12
    static let l: CGFloat = 16
    static let xl: CGFloat = 20
    static let xxl: CGFloat = 28
}

/// How things move: four tokens, none of them bounces. Under Reduce Motion a layout move jumps and a transition is a fade.
enum Motion {
    /// A row or card making room, a fold.
    static let quick = Animation.snappy(duration: 0.2)
    /// Something arriving, and the scroll that follows it.
    static let arrive = Animation.smooth(duration: 0.25)
    /// A session changing section.
    static let settle = Animation.smooth(duration: 0.3)
    /// A decision answered: its options making way for the countdown row, and back on Undo.
    static let answer = Animation.snappy(duration: 0.25)
    /// Opacity alone.
    static let fade = Animation.easeOut(duration: 0.15)
    /// "Needs you" showing up, once.
    static let needsYou = Animation.smooth(duration: 0.4)

    /// One turn of the sidebar's working ring.
    static let spinPeriod: TimeInterval = 1

    /// A ring that drains in one go, linear because it is a clock.
    static func drain(_ seconds: TimeInterval) -> Animation { .linear(duration: seconds) }

    /// A layout move: the token, or nothing under Reduce Motion.
    static func moves(_ animation: Animation, reduce: Bool) -> Animation? { reduce ? nil : animation }

    /// A change of opacity: the token, or the fade under Reduce Motion.
    static func fades(_ animation: Animation, reduce: Bool) -> Animation { reduce ? fade : animation }

    /// An element fading in or out in its place.
    static var fadeTransition: AnyTransition { .opacity.animation(fade) }

    static func needsYouTransition(reduce: Bool) -> AnyTransition { .opacity.animation(fades(needsYou, reduce: reduce)) }
}

enum Theme {
    static let ringSize: CGFloat = 14
    static let ringWidth: CGFloat = 2
    static let composerThumbnail: CGFloat = 72
    static let rowThumbnail: CGFloat = 48
    static let visualThumbnail: CGFloat = 96
    static let visualCardWidth: CGFloat = 150
    static let cardThumbnail: CGFloat = 56
    static let locale = Locale(identifier: "en_GB")
    static let time = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)

    // The Prism style (docs/design/identity-exploration.html, direction B): magenta is a decision coming back to
    // Emil, the five lane colours are tracks. Light and dark values from docs/design/prism-identity.html.
    static let question = dynamic(light: 0xD61F69, dark: 0xFF2E7E)
    static let alert = dynamic(light: 0xD63A1A, dark: 0xFF6B4A)
    static let done = dynamic(light: 0x13985A, dark: 0x3DD68C)
    /// Emil's own messages: ink, the colour the icon's beam is before it splits.
    static let ink = dynamic(light: 0x141418, dark: 0xF1F1F4)
    static let onInk = dynamic(light: 0xFFFFFF, dark: 0x18181C)
    static let queued = Color.orange
    /// A usage limit from 75 %: amber, short of the magenta that asks for Emil.
    static let warn = dynamic(light: 0xB26A00, dark: 0xF5A524)
    static let added = Color(nsColor: .systemGreen)
    static let removed = Color(nsColor: .systemRed)
    static let hairline = Color.primary.opacity(0.09)
    static let cardFill = Color.primary.opacity(0.04)

    /// Text inside a card or bubble sits at least its corner radius from the edge.
    static let cardRadius = Space.m
    static let cardPadding = Space.l
    /// Board cards sit Space.l in from the column, so their text lands Space.l + Space.m in.
    static let boardCardPadding = Space.m
    static let usageLabelWidth: CGFloat = 32
    /// How soon a follow-up from Prism joins the message before it.
    static let followUpWindow: TimeInterval = 120
    static let bubbleRadius = Space.l
    static let composerRadius = Space.l
    /// A track's decision dialog scrolls past this, so the conversation keeps most of the window.
    static let dialogMaxHeight: CGFloat = 480
    static let chipHeight: CGFloat = 16
    /// The hit area of an icon-only button.
    static let iconTarget: CGFloat = 20

    /// The conversation's readable column.
    static let chatMaxWidth: CGFloat = 720
    /// Prose wraps here, about 90 characters a line, inside the wider column.
    static let proseMaxWidth: CGFloat = 560
    static let boardMinWidth: CGFloat = 300
    static let boardIdealWidth: CGFloat = 340
    static let sidebarMinWidth: CGFloat = 220
    static let sidebarMaxWidth: CGFloat = 420
    static let dotSize: CGFloat = 7
    /// The working mark in the sidebar, a little larger than a dot as in the mockup.
    static let spinnerSize: CGFloat = 10
    static let childIndent = Space.xl
    /// The lane mark on a track row, and the tree lines down to its sessions.
    static let laneChip = CGSize(width: 3, height: 16)
    /// The lane mark beside a track row's two lines.
    static let laneChipTall: CGFloat = 28
    static let laneRail: CGFloat = 2
    static let treeRadius: CGFloat = 6
    /// How far a row's tree lines reach past its top and bottom: across the gap between sidebar rows.
    static let treeBleed = Space.m
    /// The frosted glass over the wall in the side columns (the mockup's --w-glass): the wall's colour shows through.
    /// Enough to melt the gradient's pinwheel centre into a wash, as the mockup's glass does.
    static let wallBlur: CGFloat = 64
    static let glass = dynamic(light: 0xFAFAFC, dark: 0x28282E).opacity(0.55)
    /// The sheet under the conversation (the mockup's --rv-paper): text, timestamps and cards sit on near-white or
    /// near-ink, not on the raw wall. The wall stays at full strength behind the sidebar and board.
    /// A veil, not a sheet: thin enough that the wall's gradient still shows through (it was 0.84/0.86 and read as flat
    /// white), thick enough that the wall's pastel (light) or deep (dark) stops stay quiet behind the text.
    static let paper = Color(nsColor: NSColor(name: nil) { appearance in
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(hex: 0x16161A).withAlphaComponent(0.42)
            : NSColor.white.withAlphaComponent(0.38)
    })

    /// Opens the sidebar wide enough for the longest track name (EMI-405), within the column's bounds: the name plus
    /// the lane mark, the chip and the chevron.
    static func sidebarIdealWidth(names: [String]) -> CGFloat {
        let font = NSFont.preferredFont(forTextStyle: .body)
        let longest = names.map { ($0 as NSString).size(withAttributes: [.font: font]).width }.max() ?? 0
        return min(sidebarMaxWidth, max(300, (longest + 150).rounded()))
    }

    static func lane(_ lane: Lane) -> Color {
        switch lane {
        case .pink: Color(hex: 0xFF5C93)
        case .amber: Color(hex: 0xF5A524)
        case .teal: Color(hex: 0x2EC4C9)
        case .blue: Color(hex: 0x4C8DFF)
        case .violet: Color(hex: 0x9B6BFF)
        }
    }

    static func lane(of trackID: String?) -> Color? { trackID.map { lane(Lane.of($0)) } }

    private static func dynamic(light: UInt32, dark: UInt32) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            NSColor(hex: appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? dark : light)
        })
    }

    /// "now", "2m", "3h", "2d": how long ago, as the sidebar and the board show it.
    static func ago(_ date: Date?, now: Date) -> String {
        guard let date else { return "" }
        let s = max(0, now.timeIntervalSince(date))
        switch s {
        case ..<60: return "now"
        case ..<3600: return "\(Int(s / 60))m"
        case ..<86400: return "\(Int(s / 3600))h"
        default: return "\(Int(s / 86400))d"
        }
    }

    /// A decision's title: inline Markdown with code spans at label size in mono, their hyphens non-breaking so a
    /// branch or flag never wraps mid-word.
    static func questionMarkdown(_ s: String) -> AttributedString {
        let bound = s.replacing(/`[^`]+`/) { $0.output.replacingOccurrences(of: "-", with: "\u{2011}") }
        var out = markdown(bound)
        for run in out.runs where run.inlinePresentationIntent?.contains(.code) == true {
            out[run.range].font = .prismCode
        }
        return out
    }

    /// Inline Markdown (code spans, bold, links); plain text when it doesn't parse.
    static func markdown(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
    }
}

/// How the conversation's entries are spaced: a new turn (Emil speaking) opens Space.xxl, entries in a turn sit Space.m
/// apart, and a Prism message soon after Prism's last one joins it.
enum Rhythm {
    enum Kind { case emil, prism, other }

    static func joins(_ kind: Kind, at: Date?, after prev: (kind: Kind, at: Date?)?) -> Bool {
        guard kind == .prism, let prev, prev.kind == .prism, let at, let before = prev.at else { return false }
        let elapsed = at.timeIntervalSince(before)
        return elapsed >= 0 && elapsed <= Theme.followUpWindow
    }

    static func gap(_ kind: Kind, at: Date?, after prev: (kind: Kind, at: Date?)?) -> CGFloat {
        guard prev != nil else { return 0 }
        if kind == .emil { return Space.xxl }
        return joins(kind, at: at, after: prev) ? Space.s : Space.m
    }
}

/// The type ramp: five steps. Title and Question are the only rounded ones. 10 pt (`.caption2`) is left to KeyHint and Pill.
extension Font {
    /// 15 pt: Board, a track's title.
    static let prismTitle = Font.system(.title3, design: .rounded, weight: .semibold)
    /// 13 pt semibold: a decision wherever it is drawn.
    static let prismQuestion = Font.system(.body, design: .rounded, weight: .semibold)
    /// 11 pt, tabular digits: times, ages, counts, stats.
    static let prismMeta = Font.subheadline.monospacedDigit()
    /// A decision id.
    static let prismID = Font.subheadline.monospaced().weight(.semibold)
    static let prismMono = Font.subheadline.monospaced()
    /// A code span inside a decision's title.
    static let prismCode = Font.callout.monospaced()
}

extension View {
    /// The window's title bar is transparent, so a scrolling message would print through the title; the scroll view
    /// shows only what is below the title.
    func scrollsBelowTitle() -> some View { mask(Rectangle()) }

    /// Plain text in the conversation column: shapes sit Space.m in, text Space.xxl, so text is Space.l inside the shapes' edge.
    func conversationText() -> some View { padding(.horizontal, Space.l) }

    /// Board text, headings and row marks: Space.m inside the cards' edge.
    func boardText() -> some View { padding(.horizontal, Space.m) }

    /// Message and report text: a readable measure and 18 pt lines.
    func prose() -> some View {
        lineSpacing(Space.xxs).frame(maxWidth: Theme.proseMaxWidth, alignment: .leading)
    }
}

extension NSColor {
    convenience init(hex: UInt32) {
        self.init(srgbRed: CGFloat(hex >> 16 & 0xFF) / 255, green: CGFloat(hex >> 8 & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

extension Color {
    init(hex: UInt32) { self.init(nsColor: NSColor(hex: hex)) }
}

/// A track's lane mark at the start of its row.
struct LaneChip: View {
    var color: Color
    var height = Theme.laneChip.height

    var body: some View {
        RoundedRectangle(cornerRadius: Theme.laneChip.width / 2, style: .continuous)
            .fill(color)
            .frame(width: Theme.laneChip.width, height: height)
    }
}

/// Where a child row sits in its track's tree: the depth of its elbow and which trunks run on past it.
struct TreePlace {
    /// 1 for a session under its track, 2 for a subagent under its session.
    var depth: Int
    /// This row is the last child at its depth, so its elbow ends the line.
    var last: Bool
    /// At depth 2: the track's trunk runs on past this row to a later session.
    var trunkContinues = false
    /// At depth 1: the session has subagents, so a short line drops from its mark to theirs.
    var hasChildren = false
}

/// The tree lines in the lane colour from a track down to its sessions and subagents (direction B, the mockup's
/// `.row.child`): a trunk under the parent's mark and a rounded elbow into each child's mark. Each row draws its piece
/// past its own top and bottom, across the gap between list rows, so the pieces join into one line (EMI-411).
struct TreeLines: ViewModifier {
    var color: Color?
    var place: TreePlace

    func body(content: Content) -> some View {
        content.overlay(alignment: .leading) {
            if let color {
                GeometryReader { geo in
                    let h = geo.size.height
                    let bleed = Theme.treeBleed
                    // The parent's mark is centred in a Space.l frame at the parent's indent.
                    let x = Theme.childIndent * CGFloat(place.depth - 1) + Space.l / 2
                    let mid = min(h / 2, Space.m)
                    ZStack(alignment: .topLeading) {
                        Path { p in
                            p.move(to: CGPoint(x: x, y: -bleed))
                            p.addLine(to: CGPoint(x: x, y: mid - Theme.treeRadius))
                            p.addQuadCurve(to: CGPoint(x: x + Theme.treeRadius, y: mid), control: CGPoint(x: x, y: mid))
                            p.addLine(to: CGPoint(x: Theme.childIndent * CGFloat(place.depth) + Space.xxs, y: mid))
                        }
                        .stroke(color, style: StrokeStyle(lineWidth: Theme.laneRail, lineCap: .round, lineJoin: .round))
                        Path { p in
                            if !place.last {
                                p.move(to: CGPoint(x: x, y: mid))
                                p.addLine(to: CGPoint(x: x, y: h + bleed))
                            }
                            if place.depth == 2, place.trunkContinues {
                                p.move(to: CGPoint(x: Space.l / 2, y: -bleed))
                                p.addLine(to: CGPoint(x: Space.l / 2, y: h + bleed))
                            }
                            if place.depth == 1, place.hasChildren {
                                // From under the session's own mark down to its first subagent's elbow.
                                let own = Theme.childIndent + Space.l / 2
                                p.move(to: CGPoint(x: own, y: mid + Theme.spinnerSize / 2 + Space.xxs))
                                p.addLine(to: CGPoint(x: own, y: h + bleed))
                            }
                        }
                        .stroke(color, style: StrokeStyle(lineWidth: Theme.laneRail, lineCap: .butt))
                    }
                }
                .allowsHitTesting(false)
            }
        }
    }
}

/// The wall behind the glass (direction B): the lane colours, soft, so the sidebar's glass shows the tracks' colours
/// through it. Light and dark from docs/design/prism-identity.html.
/// One wall for the whole window: each column draws its piece of it, centred at the same point of the window, so the
/// centre and the board read as one surface. Blurred as the mockup's glass blurs it, which softens the gradient's pinwheel.
struct PrismWall: View {
    @Environment(\.colorScheme) private var scheme
    @Environment(\.windowSize) private var window

    var body: some View {
        let stops: [UInt32] = scheme == .dark
            ? [0x4A2436, 0x1D3352, 0x173B2C, 0x4A3B16, 0x4A2436]
            : [0xF4B8C8, 0xB9D7F7, 0xC6F0D8, 0xF8E3A8, 0xF4B8C8]
        GeometryReader { geo in
            let frame = geo.frame(in: .global)
            let w = max(frame.width, 1), h = max(frame.height, 1)
            let size = window == .zero ? frame.size : window
            let center = UnitPoint(x: (size.width * 0.6 - frame.minX) / w, y: (size.height * 0.4 - frame.minY) / h)
            AngularGradient(colors: stops.map { Color(hex: $0) }, center: center, angle: .degrees(200 - 90))
                .blur(radius: Theme.wallBlur, opaque: true)
        }
    }
}

extension EnvironmentValues {
    /// The window's content size, for the wall's shared centre.
    @Entry var windowSize: CGSize = .zero
}

struct Dot: View {
    var color: Color
    var hollow = false

    var body: some View {
        Group {
            if hollow {
                Circle().strokeBorder(color, lineWidth: 1.2)
            } else {
                Circle().fill(color)
            }
        }
        .frame(width: Theme.dotSize, height: Theme.dotSize)
    }
}

/// The sidebar's working mark, as in the mockup (EMI-440): a ring with a quarter gap, stroked as wide as the tree lines
/// in the track's lane colour, turning once a second. The system's spinner ignores the tint and reads grey and spoked.
/// Under Reduce Motion the ring holds still; its gap still says "working" beside the dots.
///
/// The turning is a Core Animation animation, run by the render server: a TimelineView redrew every spinner from the app
/// at the display's rate, which kept an idle window at 40 % CPU with a few sessions working.
struct Spinner: View {
    var color: Color? = nil

    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        SpinnerRing(color: color ?? .secondary, turns: !reduce)
            .frame(width: Theme.spinnerSize, height: Theme.spinnerSize)
            .accessibilityElement()
            .accessibilityLabel("Working")
    }
}

/// The spinner's ring as a layer, so it turns without the app drawing a frame.
private struct SpinnerRing: NSViewRepresentable {
    var color: Color
    var turns: Bool

    func makeNSView(context: Context) -> RingView { RingView() }

    func updateNSView(_ view: RingView, context: Context) {
        view.ring.strokeColor = color.resolve(in: context.environment).cgColor
        view.turns = turns
    }

    final class RingView: NSView {
        let ring = CAShapeLayer()
        var turns = true { didSet { if turns != oldValue { updateSpin() } } }

        init() {
            super.init(frame: .zero)
            wantsLayer = true
            ring.fillColor = nil
            ring.lineWidth = Theme.laneRail
            ring.lineCap = .round
            ring.strokeEnd = 0.75
            layer?.addSublayer(ring)
        }

        @available(*, unavailable)
        required init?(coder: NSCoder) { fatalError("init(coder:) is not used") }

        /// Flipped as SwiftUI is, so the arc starts at three o'clock and runs clockwise, as Circle().trim did.
        override var isFlipped: Bool { true }

        override func layout() {
            super.layout()
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            ring.bounds = bounds
            ring.position = CGPoint(x: bounds.midX, y: bounds.midY)
            ring.path = CGPath(ellipseIn: bounds.insetBy(dx: Theme.laneRail / 2, dy: Theme.laneRail / 2), transform: nil)
            CATransaction.commit()
        }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            updateSpin()
        }

        /// Clockwise, once a period, every ring in step: each starts at the same phase of the media clock.
        private func updateSpin() {
            ring.removeAnimation(forKey: "spin")
            guard turns, window != nil else { return }
            let spin = CABasicAnimation(keyPath: "transform.rotation.z")
            spin.fromValue = 0
            spin.toValue = 2 * Double.pi
            spin.duration = Motion.spinPeriod
            spin.repeatCount = .infinity
            spin.isRemovedOnCompletion = false
            let now = CACurrentMediaTime()
            spin.beginTime = ring.convertTime(now - now.truncatingRemainder(dividingBy: Motion.spinPeriod), from: nil)
            ring.add(spin, forKey: "spin")
        }
    }
}

/// A small tinted capsule: Question, Permission, Error, Recommended, Ready to land.
struct Pill: View {
    var text: String
    var color: Color

    var body: some View {
        Text(text)
            .font(.caption2.weight(.semibold))
            .foregroundStyle(color)
            .padding(.horizontal, Space.s)
            .frame(height: Theme.chipHeight)
            .background(color.opacity(0.14), in: RoundedRectangle(cornerRadius: Space.s, style: .continuous))
    }
}

struct DiffStat: View {
    var changes: Changes

    var body: some View {
        HStack(spacing: Space.xs) {
            Text("+\(changes.added)").foregroundStyle(Theme.added)
            Text("\u{2212}\(changes.removed)").foregroundStyle(Theme.removed)
        }
        .font(.prismMeta)
    }
}

extension EnvironmentValues {
    /// Whether the digit keys answer the decision. A focused field types them instead.
    @Entry var keysLive = true
}

extension View {
    /// An icon-only button's label, with a 20 pt hit area.
    func iconTarget() -> some View {
        frame(width: Theme.iconTarget, height: Theme.iconTarget).contentShape(Rectangle())
    }
}

/// One style for every row- or card-shaped target: rest clear (a card keeps its fill), hover 5 %, pressed 9 % at once and
/// eased out over 0.12 s on release, disabled 40 %. No scale.
struct RowButtonStyle: ButtonStyle {
    /// What the target rests on: clear for a row, the card's fill, or an accent for a staged or recommended option.
    var fill: Color = .clear
    var radius = Space.s
    /// A card: its hairline darkens to 0.16 while the pointer is on it.
    var card = false

    func makeBody(configuration: Configuration) -> some View {
        Target(configuration: configuration, style: self)
    }

    private struct Target: View {
        let configuration: Configuration
        let style: RowButtonStyle
        @Environment(\.isEnabled) private var enabled
        @Environment(\.targetID) private var targetID
        @Environment(WindowModel.self) private var model: WindowModel?
        @State private var pointerOver = false

        var body: some View {
            let scripted = targetID != nil && model?.hoveredTarget == targetID
            let hovering = pointerOver || scripted
            let pressed = configuration.isPressed || (targetID != nil && model?.pressedTarget == targetID)
            let shade = pressed ? 0.09 : (hovering && enabled ? 0.05 : 0)
            let shape = RoundedRectangle(cornerRadius: style.radius, style: .continuous)
            configuration.label
                .background(shape.fill(style.fill))
                .background(shape.fill(Color.primary.opacity(shade)))
                .overlay { if style.card { shape.strokeBorder(hovering && enabled ? Color.primary.opacity(0.16) : Theme.hairline) } }
                .contentShape(shape)
                .opacity(enabled ? 1 : 0.4)
                .onHover { pointerOver = $0 }
                .animation(pressed ? nil : .easeOut(duration: 0.12), value: shade)
        }
    }
}

extension EnvironmentValues {
    /// Names a target for a demo script's hover and press (`card:D16`, `row:track:121`, `send`).
    @Entry var targetID: String?
}

extension View {
    func targetID(_ id: String) -> some View { environment(\.targetID, id) }
}

/// A sidebar row's hover and pressed shade, the strengths of RowButtonStyle's, drawn only while a demo script holds the row.
struct ScriptedShade: ViewModifier {
    var id: String
    var model: WindowModel

    func body(content: Content) -> some View {
        let shade = model.pressedTarget == id ? 0.09 : (model.hoveredTarget == id ? 0.05 : 0)
        content.background(RoundedRectangle(cornerRadius: Space.s, style: .continuous).fill(Color.primary.opacity(shade)))
    }
}

/// A keyboard shortcut hint.
struct KeyHint: View {
    var text: String
    @Environment(\.keysLive) private var live

    var body: some View {
        Text(text)
            .font(.caption2.monospaced())
            .foregroundStyle(live ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tertiary))
            .padding(.horizontal, Space.xs)
            .padding(.vertical, Space.xxs)
            .background(RoundedRectangle(cornerRadius: Space.xs).strokeBorder(Theme.hairline))
            .opacity(live ? 1 : 0.55)
    }
}

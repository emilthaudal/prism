import AppKit
import DeskCore
import SwiftUI

/// A message's text: plain paragraphs as one Text, as before; anything with blocks (code, tables, lists, headings,
/// quotes, rules) laid out block by block. Inline Markdown goes through `inline`, so D-id links keep working.
struct MessageText: View {
    var text: String
    var inline: (String) -> AttributedString = Theme.markdown

    var body: some View {
        let blocks = MarkdownCache.blocks(text)
        if MarkdownBlocks.isPlain(blocks) {
            paragraph(text)
        } else {
            VStack(alignment: .leading, spacing: Space.s) {
                ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                    view(for: block)
                }
            }
        }
    }

    private func paragraph(_ s: String) -> some View {
        Text(inline(s)).textSelection(.enabled).prose().fixedSize(horizontal: false, vertical: true)
    }

    @ViewBuilder private func view(for block: MarkdownBlock) -> some View {
        switch block {
        case .paragraph(let s): paragraph(s)
        case .code(_, let code): CodeBlock(code: code)
        case .table(let header, let aligns, let rows): TableBlock(header: header, alignments: aligns, rows: rows, inline: inline)
        case .list(let items): ListBlock(items: items, inline: inline)
        case .heading(let level, let s):
            Text(inline(s))
                .font(Self.headingFont(level))
                .textSelection(.enabled)
                .prose()
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, Space.xs)
        case .quote(let s):
            HStack(alignment: .top, spacing: Space.m) {
                RoundedRectangle(cornerRadius: Theme.laneRail / 2, style: .continuous).fill(.tertiary).frame(width: Theme.laneRail)
                Text(inline(s)).foregroundStyle(.secondary).textSelection(.enabled).prose().fixedSize(horizontal: false, vertical: true)
            }
        case .rule:
            Rectangle().fill(Theme.hairline).frame(height: 1).padding(.vertical, Space.xs)
        }
    }

    /// One step up the ramp per level, none past the title: 1 and 2 sit at the title step (1 rounded), 3 is body semibold.
    static func headingFont(_ level: Int) -> Font {
        switch level {
        case 1: .prismTitle
        case 2: .title3.weight(.semibold)
        default: .body.weight(.semibold)
        }
    }
}

private extension View {
    /// A quiet surface that reaches Space.l past the text column, as cards do, so its content lines up with the prose
    /// and sits Space.l (more than the corner radius) inside the edge.
    func blockSurface() -> some View {
        padding(Space.l)
            .background(Theme.cardFill, in: RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).strokeBorder(Theme.hairline))
            .padding(.horizontal, -Space.l)
    }
}

struct CodeBlock: View {
    var code: String
    @State private var hovering = false
    @State private var copied = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        ScrollView(.horizontal) {
            Text(code)
                .font(.prismCode)
                .textSelection(.enabled)
                .fixedSize()
                .padding(.trailing, Space.xxl)
        }
        .scrollIndicators(.automatic)
        .blockSurface()
        .overlay(alignment: .topTrailing) {
            Button(copied ? "Copied" : "Copy") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(code, forType: .string)
                copied = true
                Task {
                    try? await Task.sleep(for: .seconds(1.5))
                    copied = false
                }
            }
            .buttonStyle(.plain)
            .font(.prismMeta)
            .foregroundStyle(.secondary)
            .padding(.horizontal, Space.s)
            .padding(.vertical, Space.xs)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: Space.s, style: .continuous))
            .padding(.top, Space.s)
            .padding(.trailing, Space.s - Space.l)
            .opacity(hovering ? 1 : 0)
            .allowsHitTesting(hovering)
            .animation(Motion.fades(Motion.fade, reduce: reduce), value: hovering)
        }
        .onHover { hovering = $0 }
    }
}

struct TableBlock: View {
    var header: [String]
    var alignments: [MarkdownBlock.Align]
    var rows: [[String]]
    var inline: (String) -> AttributedString

    /// A cell wraps at this width once the table is too wide to fit.
    private static let cellMaxWidth: CGFloat = 240

    var body: some View {
        ViewThatFits(in: .horizontal) {
            grid(capped: false)
            ScrollView(.horizontal) { grid(capped: true) }
        }
        .blockSurface()
    }

    private func grid(capped: Bool) -> some View {
        Grid(alignment: .topLeading, horizontalSpacing: Space.l, verticalSpacing: Space.s) {
            GridRow {
                ForEach(Array(header.enumerated()), id: \.offset) { c, s in
                    cell(s, column: c, capped: capped).fontWeight(.semibold)
                }
            }
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                Rectangle().fill(Theme.hairline).frame(height: 1).gridCellColumns(max(header.count, 1))
                GridRow {
                    ForEach(Array(row.enumerated()), id: \.offset) { c, s in cell(s, column: c, capped: capped) }
                }
            }
        }
    }

    private func cell(_ s: String, column: Int, capped: Bool) -> some View {
        let align = column < alignments.count ? alignments[column] : .leading
        return Text(inline(s))
            .textSelection(.enabled)
            .multilineTextAlignment(align == .trailing ? .trailing : align == .center ? .center : .leading)
            .fixedSize(horizontal: !capped, vertical: true)
            .frame(maxWidth: capped ? Self.cellMaxWidth : nil, alignment: align == .trailing ? .trailing : align == .center ? .center : .leading)
    }
}

struct ListBlock: View {
    var items: [MarkdownBlock.Item]
    var inline: (String) -> AttributedString

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    Text(item.ordinal.map { "\($0)." } ?? (item.depth == 0 ? "\u{2022}" : "\u{25E6}"))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                        .frame(minWidth: Space.l, alignment: .trailing)
                    Text(inline(item.text)).textSelection(.enabled).lineSpacing(Space.xxs).fixedSize(horizontal: false, vertical: true)
                }
                .padding(.leading, Theme.childIndent * CGFloat(item.depth))
                .frame(maxWidth: Theme.proseMaxWidth, alignment: .leading)
            }
        }
    }
}

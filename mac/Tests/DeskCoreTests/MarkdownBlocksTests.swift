import Testing
@testable import DeskCore

@Suite struct MarkdownBlocksTests {
    @Test func fencedCodeKeepsItsLanguageAndLines() {
        let blocks = MarkdownBlocks.parse("Run:\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\nDone.")
        #expect(blocks == [
            .paragraph("Run:"),
            .code(language: "go", text: "func main() {\n\tprintln(\"hi\")\n}"),
            .paragraph("Done."),
        ])
    }

    @Test func fenceWithoutLanguage() {
        #expect(MarkdownBlocks.parse("```\nplain\n```") == [.code(language: nil, text: "plain")])
    }

    @Test func unterminatedFenceRunsToTheEnd() {
        #expect(MarkdownBlocks.parse("```sh\nmake test\n# not a heading\n\n") == [.code(language: "sh", text: "make test\n# not a heading")])
    }

    @Test func blankLinesInsideAFenceStay() {
        #expect(MarkdownBlocks.parse("```\na\n\nb\n```") == [.code(language: nil, text: "a\n\nb")])
    }

    @Test func tableWithAlignments() {
        let md = "| Name | Count | Note |\n|:--|--:|:-:|\n| pantry | 3 | ok |\n| recipe-search | 12 |"
        #expect(MarkdownBlocks.parse(md) == [.table(
            header: ["Name", "Count", "Note"],
            alignments: [.leading, .trailing, .center],
            rows: [["pantry", "3", "ok"], ["recipe-search", "12", ""]]
        )])
    }

    @Test func tableWithoutOuterPipesAndEscapedPipe() {
        let md = "a | b\n--- | ---\nx \\| y | z"
        #expect(MarkdownBlocks.parse(md) == [.table(header: ["a", "b"], alignments: [.leading, .leading], rows: [["x | y", "z"]])])
    }

    @Test func aPipeLineThatIsNotATableStaysAParagraph() {
        let text = "use a | b to pipe\nand c | d too"
        #expect(MarkdownBlocks.parse(text) == [.paragraph(text)])
        let mismatched = "| a | b |\n|---|\n| 1 | 2 |"
        #expect(MarkdownBlocks.parse(mismatched) == [.paragraph(mismatched)])
    }

    @Test func bulletAndNumberedItemsWithOneNestingLevel() {
        let md = "- one\n  - nested\n- two\n\n1. first\n2. second"
        #expect(MarkdownBlocks.parse(md) == [
            .list([.init(text: "one"), .init(depth: 1, text: "nested"), .init(text: "two")]),
            .list([.init(ordinal: 1, text: "first"), .init(ordinal: 2, text: "second")]),
        ])
    }

    @Test func listItemKeepsItsWrappedLine() {
        #expect(MarkdownBlocks.parse("- one\n  more of one\n- two") == [.list([.init(text: "one\nmore of one"), .init(text: "two")])])
    }

    @Test func boldAtTheStartOfALineIsNotABullet() {
        #expect(MarkdownBlocks.parse("**Done** and more") == [.paragraph("**Done** and more")])
    }

    @Test func headingsLevelOneToThree() {
        #expect(MarkdownBlocks.parse("# A\n## B ##\n### C\n#### D\n#nospace") == [
            .heading(level: 1, text: "A"),
            .heading(level: 2, text: "B"),
            .heading(level: 3, text: "C"),
            .heading(level: 3, text: "D"),
            .paragraph("#nospace"),
        ])
    }

    @Test func quoteAndRule() {
        #expect(MarkdownBlocks.parse("> first\n> second\n\n---\n***") == [.quote("first\nsecond"), .rule, .rule])
    }

    @Test func unrecognisedTextRoundTripsUnchanged() {
        for text in ["Plain words.", "two\nlines of text", "a * b and c - d", "```not a fence``` inline", "~~strike~~ it", "<b>html</b>"] {
            #expect(MarkdownBlocks.parse(text) == [.paragraph(text)])
        }
    }

    @Test func paragraphsSplitAtBlankLines() {
        #expect(MarkdownBlocks.parse("one\ntwo\n\nthree") == [.paragraph("one\ntwo"), .paragraph("three")])
        #expect(MarkdownBlocks.isPlain(MarkdownBlocks.parse("one\n\nthree")))
    }

    @Test func mixedMessageKeepsItsOrder() {
        let md = """
        ## Result

        Two changes:
        - parser
        - view

        ```go
        x := 1
        ```

        | a | b |
        |---|---|
        | 1 | 2 |

        > note
        ---
        Bye
        """
        #expect(MarkdownBlocks.parse(md) == [
            .heading(level: 2, text: "Result"),
            .paragraph("Two changes:"),
            .list([.init(text: "parser"), .init(text: "view")]),
            .code(language: "go", text: "x := 1"),
            .table(header: ["a", "b"], alignments: [.leading, .leading], rows: [["1", "2"]]),
            .quote("note"),
            .rule,
            .paragraph("Bye"),
        ])
        #expect(!MarkdownBlocks.isPlain(MarkdownBlocks.parse(md)))
    }

    @MainActor @Test func cacheReturnsTheSameBlocks() {
        #expect(MarkdownCache.blocks("# T") == [.heading(level: 1, text: "T")])
        #expect(MarkdownCache.blocks("# T") == MarkdownBlocks.parse("# T"))
    }
}

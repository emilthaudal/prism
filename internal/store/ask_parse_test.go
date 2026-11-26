package store

import (
	"reflect"
	"testing"
)

func labels(opts ...string) []QOption {
	var out []QOption
	for _, o := range opts {
		out = append(out, QOption{Label: o})
	}
	return out
}

func TestParseAskProseLists(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		opts []QOption
		rec  int
	}{
		"D1 from the screenshot": {"Which icon for the Pantry Workspace: owl looking back, U-turn parcel, round trip or mended parcel? Recommend: owl looking back, the parcel owl's sibling.",
			labels("owl looking back", "U-turn parcel", "round trip", "mended parcel"), 1},
		"serial comma":                  {"Pick one: a, b, or c? Recommend: C because it is short", labels("a", "b", "c"), 3},
		"two items":                     {"Merge: now or later? Recommend: later.", labels("now", "later"), 2},
		"no recommendation":             {"Colour: red, green or blue?", labels("red", "green", "blue"), 0},
		"recommendation off list":       {"Colour: red, green or blue? Recommend: purple", labels("red", "green", "blue"), 0},
		"label prefix needs a word end": {"Size: red, redder or reddest? Recommend: reddest of all", labels("red", "redder", "reddest"), 3},
		"only the last sentence":        {"Context: CI is red. Which one: the old way, the new way or neither?", labels("the old way", "the new way", "neither"), 0},
		"nine items":                    {"Pick: a, b, c, d, e, f, g, h or i?", labels("a", "b", "c", "d", "e", "f", "g", "h", "i"), 0},
	} {
		got := ParseAsk(tc.in)
		if !reflect.DeepEqual(got.Options, tc.opts) || got.Recommended != tc.rec {
			t.Errorf("%s: got %+v %d, want %+v %d", name, got.Options, got.Recommended, tc.opts, tc.rec)
		}
	}
}

func TestParseAskLeavesWhatIsNotAProseList(t *testing.T) {
	for name, in := range map[string]string{
		"path misread from last night": "Read Documents/la-delivery-model-audit first",
		"path items":                   "Which repo: a/b, c/d or e/f?",
		"tilde item":                   "Where: ~/x, ~/y or ~/z?",
		"recommendation with numbers":  "Recommend: 1, 2 and 5 first.",
		"no colon":                     "Should we ship a, b or c?",
		"no question mark":             "Options: a, b or c.",
		"colon in an earlier sentence": "Context: a, b or c. Ship it?",
		"two items with a comma":       "Pick: a, or b?",
		"no final or":                  "Pick: a, b, c?",
		"long item":                    "Pick: one two three four five six seven, b or c?",
		"item with an inner or":        "Pick: a or b, c or d, e?",
		"ten items":                    "Pick: a, b, c, d, e, f, g, h, i or j?",
		"numbers only":                 "Which first: 1, 2 or 5?",
		"punctuated item":              "Pick: a (old), b or c?",
		"file item":                    "Edit: main.go, util.go or x.go?",
		"empty stem":                   ": a, b or c?",
		"plain":                        "Why is it red?",
	} {
		if got := ParseAsk(in); len(got.Options) != 0 {
			t.Errorf("%s: %q parsed as %+v", name, in, got.Options)
		}
	}
}

func TestParseAskAttachments(t *testing.T) {
	t.Setenv("HOME", "/Users/emil")
	whole := ParseAsk("Which icon? See: ~/x/sheet.png Recommend: 1")
	eq(t, "question-wide", []any{whole.Question, whole.Attachments, whole.Recommendation}, []any{"Which icon?",
		[]Attachment{{"sheet.png", "file:///Users/emil/x/sheet.png", "image"}}, "1"})

	per := ParseAsk("Which icon? 1) owl looking back (see: ~/x/owl.png) 2) U-turn parcel (see: https://example.com/u.html) 3) plain Recommend: 1")
	eq(t, "per option", []any{per.Question, per.Options, per.Attachments, per.Recommended}, []any{"Which icon?", []QOption{
		{Label: "owl looking back", Attachment: &Attachment{"owl.png", "file:///Users/emil/x/owl.png", "image"}},
		{Label: "U-turn parcel", Attachment: &Attachment{"example.com", "https://example.com/u.html", "page"}},
		{Label: "plain"}}, []Attachment(nil), 1})

	for in, want := range map[string]Attachment{
		"Q? See: /tmp/a.JPG.":          {"a.JPG", "file:///tmp/a.JPG", "image"},
		"Q? See: /tmp/a.heic":          {"a.heic", "file:///tmp/a.heic", "image"},
		"Q? See: /tmp/a.svg":           {"a.svg", "file:///tmp/a.svg", "image"},
		"Q? See: /tmp/page.htm":        {"page.htm", "file:///tmp/page.htm", "page"},
		"Q? See: /tmp/notes.md":        {"notes.md", "file:///tmp/notes.md", "file"},
		"Q? See: https://x.io/a/b.png": {"x.io", "https://x.io/a/b.png", "image"},
		"Q? See: https://x.io/a":       {"x.io", "https://x.io/a", "page"},
		"Q? (see: /tmp/my shot.png)":   {"my shot.png", "file:///tmp/my%20shot.png", "image"},
	} {
		got := ParseAsk(in)
		if got.Question != "Q?" || !reflect.DeepEqual(got.Attachments, []Attachment{want}) {
			t.Errorf("%q: got %q %+v, want %+v", in, got.Question, got.Attachments, want)
		}
	}

	both := ParseAsk("Which? 1) a (see: /tmp/a.png) (see: /tmp/a2.png) 2) b See: /tmp/sheet.png")
	eq(t, "extra refs go to the question", []any{both.Options[0].Attachment.Label, len(both.Attachments)}, []any{"a.png", 2})

	for _, in := range []string{"Which? See: below", "Which? (see: the docs)", "Which? See: docs/a.png"} {
		if got := ParseAsk(in); len(got.Attachments) != 0 || got.Question != in {
			t.Errorf("%q treated as a reference: %+v", in, got)
		}
	}
}

func TestAttachmentsSurviveTheStore(t *testing.T) {
	s := newStore(t)
	mustTrack(t, s, "alpha")
	ask := "Icon? 1) owl (see: /tmp/owl.png) 2) parcel See: /tmp/sheet.png Recommend: 1"
	if _, err := s.Card("alpha", CardUpdate{Asks: []string{ask}}); err != nil {
		t.Fatal(err)
	}
	qs, _ := s.OpenQuestions()
	if len(qs) != 1 {
		t.Fatalf("%d questions", len(qs))
	}
	eq(t, "stored", []any{qs[0].Question, qs[0].Options[0].Attachment, qs[0].Attachments},
		[]any{"Icon?", &Attachment{"owl.png", "file:///tmp/owl.png", "image"}, []Attachment{{"sheet.png", "file:///tmp/sheet.png", "image"}}})
}

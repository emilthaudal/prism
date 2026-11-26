package tracks

import "testing"

// A pane write keeps the card's mtime, and swapping one session id for another of the same
// length keeps its size; the pane must still see the change.
func TestFileStampSeesSameSizeRewrite(t *testing.T) {
	setup(t)
	metaCard(t, "x", "focus", "rex_session: session:aaaa")
	before := fileStamp()
	if err := setMeta("x", "rex_session", "session:bbbb"); err != nil {
		t.Fatal(err)
	}
	if fileStamp() == before {
		t.Error("the stamp did not change after a same-size rewrite")
	}
}

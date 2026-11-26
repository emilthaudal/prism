package tracks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/emilthaudal/prism/internal/store"
)

const (
	leadSess  = "session:0010-lead-aaaa"
	childSess = "session:0011-kid-bbbb"
)

// leadHome makes a tracks.db with an ordinary track "plain" and a lead "boss" in leadSess.
func leadHome(t *testing.T) (th, bin string) {
	t.Helper()
	th, bin = setup(t)
	card(t, th, "plain", "focus", "session:x-sa", "w", "")
	card(t, th, "boss", "focus", leadSess, "w", "")
	importNow(t)
	ranOnTheStore(t)
	if out, code := cardIn("role: lead\n", "card", "boss"); code != 0 {
		t.Fatalf("role: lead: %d %s", code, out)
	}
	return th, bin
}

func lsTracks(t *testing.T) map[string]lsTrack {
	t.Helper()
	out, code := do("ls", "--json")
	if code != 0 {
		t.Fatalf("ls --json: %d %s", code, out)
	}
	var doc lsDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("ls --json is not json: %v\n%s", err, out)
	}
	m := map[string]lsTrack{}
	for _, tr := range doc.Tracks {
		m[tr.Name] = tr
	}
	return m
}

// addThread makes a thread of boss the way tracks spawn does, without starting a session.
func addThread(t *testing.T, name string) {
	t.Helper()
	err := withStore(func(st *store.Store) error {
		_, err := st.Spawn("boss", store.SpawnSpec{Name: name, Dir: t.TempDir(), Goal: "do " + name})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRoleAndParentRoundTrip(t *testing.T) {
	leadHome(t)
	addThread(t, "kid")

	ls := lsTracks(t)
	str := func(p *string) string {
		if p == nil {
			return "<null>"
		}
		return *p
	}
	for name, want := range map[string][2]string{
		"plain": {"<null>", "<null>"},
		"boss":  {"lead", "<null>"},
		"kid":   {"<null>", "boss"},
	} {
		if got := [2]string{str(ls[name].Role), str(ls[name].Parent)}; got != want {
			t.Errorf("ls --json %s: role, parent = %v, want %v", name, got, want)
		}
	}

	raw, _ := do("ls", "--json")
	for _, key := range []string{`"role": null`, `"parent": null`} {
		if !strings.Contains(strings.ReplaceAll(raw, `":null`, `": null`), key) {
			t.Errorf("ls --json has no %s on an unset entry", key)
		}
	}

	view, err := os.ReadFile(cardPath("kid"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(view), "\nparent: boss\n") {
		t.Errorf("the card view of kid does not show its parent:\n%s", view)
	}
	view, _ = os.ReadFile(cardPath("boss"))
	if !strings.Contains(string(view), "\nrole: lead\n") {
		t.Errorf("the card view of boss does not show role: lead:\n%s", view)
	}

	// Reopened from tracks.db alone: the values come from the database, not from the views.
	if err := os.Remove(cardPath("kid")); err != nil {
		t.Fatal(err)
	}
	var v store.View
	if err := withStore(func(st *store.Store) (err error) { v, err = st.Get("kid"); return }); err != nil {
		t.Fatal(err)
	}
	if v.Track.Parent() != "boss" || v.Track.Tier != "" {
		t.Errorf("kid from the database: parent %q tier %q", v.Track.Parent(), v.Track.Tier)
	}
}

func TestRoleCanBeClearedAndIsLeadOrNothing(t *testing.T) {
	leadHome(t)
	if out, code := cardIn("role: boss\n", "card", "plain"); code == 0 || !strings.Contains(out, "role must be lead") {
		t.Errorf("role: boss was accepted: %d %s", code, out)
	}
	if out, code := cardIn("role:\n", "card", "boss"); code != 0 {
		t.Fatalf("clearing the role: %d %s", code, out)
	}
	if r := lsTracks(t)["boss"].Role; r != nil {
		t.Errorf("role after clearing = %q, want null", *r)
	}
}

func TestAThreadCannotBecomeALead(t *testing.T) {
	leadHome(t)
	addThread(t, "kid")
	if out, code := cardIn("role: lead\n", "card", "kid"); code == 0 || !strings.Contains(out, "thread cannot be a lead") {
		t.Errorf("a thread made itself a lead: %d %s", code, out)
	}
	if r := lsTracks(t)["kid"].Role; r != nil {
		t.Errorf("kid has role %q", *r)
	}
}

func TestRenamingALeadMovesItsThreads(t *testing.T) {
	leadHome(t)
	addThread(t, "kid")
	if out, code := do("rename", "boss", "chief"); code != 0 {
		t.Fatalf("rename: %d %s", code, out)
	}
	ls := lsTracks(t)
	if p := ls["kid"].Parent; p == nil || *p != "chief" {
		t.Errorf("kid's parent after the lead was renamed = %v, want chief", p)
	}
	if r := ls["chief"].Role; r == nil || *r != "lead" {
		t.Errorf("chief lost its role: %v", r)
	}
}

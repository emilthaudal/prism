package tracks

import "testing"

func TestEffectiveMode(t *testing.T) {
	waitCard := func(mode, check, on, needs string) *Card {
		return &Card{Name: "x", Meta: map[string]string{"mode": mode, "check": check, "waiting_on": on},
			Sections: map[string]string{"Needs me": needs}}
	}
	for _, c := range []struct {
		name    string
		card    *Card
		working bool
		want    string
	}{
		{"check set, nothing else", waitCard("background", "pr 4 merged", "", ""), false, "waiting"},
		{"waiting_on set", waitCard("background", "", "Rasmus", ""), false, "waiting"},
		{"an ask is open", waitCard("background", "pr 4 merged", "", "1. Merge? Recommend: yes."), false, "background"},
		{"a session is working", waitCard("background", "pr 4 merged", "", ""), true, "background"},
		{"nothing to wait on", waitCard("background", "", "", ""), false, "background"},
		{"stored waiting reads as background", waitCard("waiting", "", "", ""), false, "background"},
		{"stored waiting with a check shows waiting", waitCard("waiting", "pr 4 merged", "", ""), false, "waiting"},
		{"focus stays focus while it waits", waitCard("focus", "pr 4 merged", "", ""), false, "focus"},
		{"parked stays parked", waitCard("parked", "pr 4 merged", "", ""), false, "parked"},
	} {
		if got := effectiveMode(c.card, c.working); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

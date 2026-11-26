package tracks

import "testing"

func TestHomeTargetGoesHomeAndBack(t *testing.T) {
	live := []rexSession{{"s:home", "tracks"}, {"s:a", "a"}, {"s:b", "b"}}
	for _, c := range []struct {
		name, from, last string
		target           string
		back             bool
	}{
		{"from an agent goes home", "s:a", "s:b", "s:home", false},
		{"from home goes back", "s:home", "s:a", "s:a", true},
		{"from home with the last session gone stays", "s:home", "s:gone", "", true},
		{"from home with no last session stays", "s:home", "", "", true},
		{"never back to home itself", "s:home", "s:home", "", true},
		{"no from (palette, shell) goes home", "", "s:a", "s:home", false},
	} {
		got, back := homeTarget(c.from, "s:home", c.last, live)
		if got != c.target || back != c.back {
			t.Errorf("%s: got %q back=%v, want %q back=%v", c.name, got, back, c.target, c.back)
		}
	}
}

func TestHomeCmdRejectsUnknownArgs(t *testing.T) {
	if err := homeCmd([]string{"--bogus"}, nil); err == nil {
		t.Fatal("want a usage error")
	}
}

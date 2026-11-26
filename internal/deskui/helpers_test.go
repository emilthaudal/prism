package deskui

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fixturePath is mock/serve-fixture.json at the repo root.
func fixturePath(t *testing.T) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "mock", "serve-fixture.json")
}

var fixtureNow = time.Date(2026, 11, 24, 13, 32, 0, 0, time.UTC)

// testModel is the window on the fixture at w×h, with the snapshot loaded and a clock that
// tests move by hand.
type testModel struct {
	*Model
	t      *testing.T
	f      *Fixture
	clock  time.Time
	opened []string
}

func newTest(t *testing.T, w, h int) *testModel {
	t.Helper()
	f, err := LoadFixture(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	tm := &testModel{t: t, f: f, clock: fixtureNow}
	f.now = func() time.Time { return tm.clock }
	m := New(f)
	m.now = func() time.Time { return tm.clock }
	m.loc = time.UTC
	m.open = func(u string) error { tm.opened = append(tm.opened, u); return nil }
	tm.Model = m
	t.Cleanup(m.cancel)
	snap, _ := f.Snapshot(context.Background())
	tm.send(tea.WindowSizeMsg{Width: w, Height: h})
	tm.send(snapshotMsg{snap})
	return tm
}

// send delivers a message and runs the commands it returns, as the program would, except
// the event pump and the clock tick.
func (tm *testModel) send(msg tea.Msg) {
	tm.t.Helper()
	_, cmd := tm.Update(msg)
	tm.run(cmd)
}

func (tm *testModel) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil, tickMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			tm.run(c)
		}
	default:
		tm.send(msg)
	}
}

var named = map[string]tea.KeyPressMsg{
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEscape},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"backspace": {Code: tea.KeyBackspace},
	"space":     {Code: tea.KeySpace, Text: " "},
	"alt+1":     {Code: '1', Mod: tea.ModAlt},
	"alt+2":     {Code: '2', Mod: tea.ModAlt},
	"ctrl+z":    {Code: 'z', Mod: tea.ModCtrl},
	"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
}

func (tm *testModel) press(keys ...string) {
	tm.t.Helper()
	for _, k := range keys {
		msg, ok := named[k]
		if !ok {
			msg = tea.KeyPressMsg{Text: k, Code: []rune(k)[0]}
		}
		tm.send(msg)
	}
}

func (tm *testModel) typeText(s string) {
	for _, r := range s {
		tm.press(string(r))
	}
}

// advance moves the clock on and delivers a tick.
func (tm *testModel) advance(d time.Duration) {
	tm.clock = tm.clock.Add(d)
	tm.send(tickMsg{})
}

// screen is the window as plain text, trailing spaces trimmed.
func (tm *testModel) screen() string {
	lines := strings.Split(ansi.Strip(tm.Render()), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

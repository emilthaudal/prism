package tracks

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var update = flag.Bool("update", false, "rewrite golden files")

const pantryCard = `---
name: Finalize-Pantry-decision-logs
mode: focus
tier: 1
dir: %DIR%
links: PAN-88, PAN-89, PAN-91, PAN-92, https://example.atlassian.net/wiki/spaces/ENG/pages/1001/Pantry-decision-log, https://example.atlassian.net/wiki/spaces/ENG/pages/1002/Pantry-sync-decision-log
---
## Goal
Finalize the open Pantry platform and Pantry integration decision logs so the build teams can start on the agreed shape of both, with every open decision recorded or owned.

## Where it is
Platform log (PAN-88): D1, D2 decided; D3, D4, D7 still open.
Integration log (PAN-89): all 8 decided.
Tier 1: docs only, no code.

## Needs me
1. D7 agent login: record "C, Entra SSO built in PAN-141; franchisees keep login links until they need access"? Recommend: yes
2. D3 Returns context: you put A with no name. Record it as yours, or wait for Robin in the PAN-92 canvas review? Recommend: wait for Robin: his decision, and it needs an ADR

## Standing orders
- Commit.
`

func putCard(t *testing.T, th, name, body string) {
	t.Helper()
	body = strings.ReplaceAll(body, "%DIR%", th)
	foldBack(t)
	if err := writeAtomic(cardPath(name), []byte(body)); err != nil {
		t.Fatal(err)
	}
}

// rightCol is the detail column of a rendered line, and ok is false for a line without one.
func rightCol(l string) (string, bool) {
	_, r, ok := strings.Cut(l, " │ ")
	return r, ok
}

func paneWith(t *testing.T, w, h int) *paneModel {
	t.Helper()
	m := newPane()
	m.now = time.Now()
	m.Update(sinceMsg{name: m.cur, err: errNotGit.Error(), at: m.now})
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func TestPantryExample160(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "Finalize-Pantry-decision-logs", pantryCard)
	m := paneWith(t, 160, 45)
	m.now = m.cards[0].Mtime.Add(30 * time.Second) // the where-line's age, "0m", must not depend on the clock
	out := plain(m, 160, 45)
	t.Logf("\n%s", out)
	var right []string
	for _, l := range strings.Split(out, "\n") {
		if r, ok := rightCol(l); ok {
			right = append(right, strings.TrimRight(r, " "))
		}
	}
	got := strings.Join(right, "\n")
	for _, want := range []string{
		"Finalize-Pantry-decision-logs",
		"T1 · focus",
		"\nGOAL\nFinalize the open Pantry platform and Pantry integration decision logs so\nthe build teams can start on the agreed shape of both, with every open …\nLinks  PAN-88",
		"\n\nPlatform log (PAN-88): D1, D2 decided; D3, D4, D7 still open.\nIntegration log (PAN-89): all 8 decided.\n\n┃ Needs me",
		"┃ Needs me  1-2 accept · r reply\n┃\n┃ 1  D7 agent login: record \"C, Entra SSO built in PAN-141; franchisees keep\n┃    login links until they need access\"?\n┃    → yes\n┃\n┃ 2  D3 Returns context:",
		"┃    → wait for Robin: his decision, and it needs an ADR\n",
		"Links  PAN-88 · PAN-89 · PAN-91 · PAN-92 · 2 Confluence pages",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Tier 1") || strings.Contains(got, "Goal") || strings.Contains(got, "atlassian") || strings.Contains(got, "Since you") || strings.Contains(got, "First look") {
		t.Errorf("hidden text shown:\n%s", got)
	}
	golden := filepath.Join("testdata", "pantry_160"+storeSuffix()+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != out {
		t.Errorf("render differs from %s (go test -run PantryExample -update):\n%s", golden, out)
	}
}

const longURL = "https://example.com/workspaces/acme/projects/q3-launch/docs/9f8e7d6c5b4a/9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f/comments?tab=comments&filter=unresolved&page=2"

func worstCards() map[string]string {
	asks := ""
	for i := 1; i <= 9; i++ {
		asks += string(rune('0'+i)) + ". Decision number " + string(rune('0'+i)) + " with a question that needs an answer? Recommend: option " + string(rune('A'+i-1)) + ".\n"
	}
	long := strings.Repeat("The quick brown fox jumps over the lazy dog and keeps running. ", 5)[:300]
	return map[string]string{
		"nine-asks":    "---\nname: nine-asks\nmode: focus\ntier: 3\ndir: %DIR%\n---\n## Goal\nG\n\n## Where it is\nState.\n\n## Needs me\n" + asks,
		"long-url":     "---\nname: long-url\nmode: focus\ndir: %DIR%\n---\n## Goal\nG\n\n## Where it is\nState.\n\n## Needs me\n1. " + long + " " + longURL + " Recommend: " + longURL + "\n2. " + longURL + "\n",
		"unicode":      "---\nname: unicode\nmode: focus\ntier: 2\ndir: %DIR%\nlinks: https://example.org/王秀英\n---\n## Goal\n王秀英のゴール 🦊 👩🏽‍💻 نور الهدى عبد الرحمن Benachrichtigungseinstellungen Benachrichtigungseinstellungen Benachrichtigungseinstellungen\n\n## Where it is\n王秀英 は 完了しました。👩🏽‍💻 Đặng Thị Ngọc Hân 🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊\n\n## Needs me\n1. 王秀英のログインを記録しますか？これは長い質問で、折り返しが必要です。名前は 🦊 Fox さんです。Recommend: はい、記録する🦊\n2. Ólafur Darri Ólafsson Aleksandra Wiśniewska-Kowalczyk? Recommend: yes\n",
		"empty":        "---\nname: empty\nmode: focus\ndir: %DIR%\n---\n## Goal\n\n## Where it is\n\n## Needs me\n\n## Standing orders\n",
		"no-recommend": "---\nname: no-recommend\nmode: focus\ndir: %DIR%\n---\n## Goal\nG\n\n## Where it is\nState.\n\n## Needs me\n1. Should the two logs share one ADR template or keep their own, given that the integration log already has eight decided entries and the platform log has three open ones?\n2. Check \"pr 4 approved\" is now yes. Recommend: set mode to focus.\n   a continuation line of the second ask that belongs to it\n",
		"a-very-long-track-name-that-goes-on-and-on-and-on-and-on-and-on-and-on-and-on-and-on": "---\nname: a-very-long-track-name-that-goes-on-and-on-and-on-and-on-and-on-and-on-and-on-and-on\nmode: focus\ntier: 3\ndir: %DIR%\nlinks: PAN-1, https://example.atlassian.net/wiki/spaces/A/pages/1, https://sub.department.region.example.test/a/b, https://example.com/" + strings.Repeat("x", 200) + "\n---\n",
	}
}

func TestWorstCaseNeverOverflows(t *testing.T) {
	for name, body := range worstCards() {
		for _, size := range [][2]int{{80, 24}, {160, 45}, {100, 12}} {
			w, h := size[0], size[1]
			th, _ := setup(t)
			putCard(t, th, name, body)
			m := paneWith(t, w, h)
			rw, _ := m.detailGeom()
			mw := min(rw, maxMeasure)
			for step := range 8 {
				out := plain(m, w, h)
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Fatalf("%s %dx%d: height %d, want %d", name, w, h, len(lines), h)
				}
				for i, l := range lines {
					if lw := lipgloss.Width(l); lw > w {
						t.Errorf("%s %dx%d: line %d is %d wide: %q", name, w, h, i, lw, l)
					}
					r, ok := rightCol(l)
					if !ok || i < 2 || i >= h-2 {
						continue
					}
					if rwid := lipgloss.Width(strings.TrimRight(r, " ")); rwid > rw {
						t.Errorf("%s %dx%d: detail line %d is %d wide, panel %d: %q", name, w, h, i, rwid, rw, r)
					} else if rwid > mw && i > 2 {
						t.Errorf("%s %dx%d: body line %d is %d wide, measure %d: %q", name, w, h, i, rwid, mw, r)
					}
				}
				if step == 0 && w == 160 && !strings.Contains(out, name[:min(len(name), 12)]) {
					t.Errorf("%s: name missing:\n%s", name, out)
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			}
		}
	}
}

func TestAsksAlignAndScrollReachesEnd(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "nine-asks", worstCards()["nine-asks"])
	m := paneWith(t, 80, 24)
	out := plain(m, 80, 24)
	if !strings.Contains(out, "↓ more") {
		t.Fatalf("nine asks at 24 rows must scroll:\n%s", out)
	}
	if !strings.Contains(out, "1-9 accept · r reply") && !strings.Contains(out, "1-9") {
		t.Logf("hint dropped at narrow width:\n%s", out)
	}
	for range 10 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	out = plain(m, 80, 24)
	if !strings.Contains(out, "→ option I.") || strings.Contains(out, "↓ more") {
		t.Errorf("scrolled to the end:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		r, ok := rightCol(l)
		if !ok {
			continue
		}
		r = unbar(strings.TrimRight(r, " "))
		if strings.HasPrefix(r, "→") {
			t.Errorf("arrow must sit under the ask text: %q", r)
		}
		if strings.Contains(r, "→") && !strings.HasPrefix(r, "   → ") {
			t.Errorf("arrow misaligned: %q", r)
		}
	}
}

// unbar drops the Needs me section's left bar, so asks read as they would without it.
func unbar(r string) string {
	if r == "┃" {
		return ""
	}
	return strings.TrimPrefix(r, "┃ ")
}

func TestLongAsksHangUnderText(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "long-url", worstCards()["long-url"])
	m := paneWith(t, 160, 45)
	out := plain(m, 160, 45)
	var asks []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		r, ok := rightCol(l)
		if !ok {
			continue
		}
		r = unbar(strings.TrimRight(r, " "))
		if strings.HasPrefix(r, "Needs me") {
			in = true
			continue
		}
		if strings.HasPrefix(r, "GOAL") { // the section after Needs me
			break
		}
		if in {
			asks = append(asks, strings.TrimRight(r, " "))
		}
	}
	if len(asks) < 6 {
		t.Fatalf("asks:\n%s", strings.Join(asks, "\n"))
	}
	for _, l := range asks {
		if l == "" {
			continue
		}
		if !strings.HasPrefix(l, "1  ") && !strings.HasPrefix(l, "2  ") && !strings.HasPrefix(l, "   ") {
			t.Errorf("wrapped line not indented under the text: %q", l)
		}
	}
}

func TestLinksCollapse(t *testing.T) {
	ls := parseLinks("PAN-1, https://a.atlassian.net/wiki/spaces/X/pages/1, https://sub.example.test/a/b?x=1, https://a.atlassian.net/wiki/x, https://github.com/o/r/pull/9, https://a.atlassian.net/jira/software")
	got := strings.Join(linkLabels(ls), " | ")
	if want := "PAN-1 | sub.example.test | PR #9 | a.atlassian.net | 2 Confluence pages"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if one := linkLabels(parseLinks("https://a.atlassian.net/wiki/x")); len(one) != 1 || one[0] != "Confluence" {
		t.Errorf("one page: %v", one)
	}
	if len(ls) != 6 || ls[1].url != "https://a.atlassian.net/wiki/spaces/X/pages/1" {
		t.Errorf("every link stays openable: %v", ls)
	}
}

func TestNoGitDirOmitsSinceButRealErrorsShow(t *testing.T) {
	th, _ := setup(t)
	putCard(t, th, "x", "---\nname: x\nmode: focus\ndir: "+th+"\n---\n## Where it is\nw\n")
	m := newPane()
	m.now = time.Now()
	m.Update(sinceMsg{name: "x", err: errNotGit.Error()})
	if out := plain(m, 120, 20); strings.Contains(out, "First look") || strings.Contains(out, "Since you") || strings.Contains(out, "git") {
		t.Errorf("not-git shown:\n%s", out)
	}
	m.Update(sinceMsg{name: "x", err: "exit status 1: fatal: bad object"})
	if out := plain(m, 120, 20); !strings.Contains(out, "git: exit status 1: fatal: bad object") {
		t.Errorf("real git error hidden:\n%s", out)
	}
}

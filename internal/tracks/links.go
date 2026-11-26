package tracks

import (
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

const (
	linkCycle = 2 * time.Second
	flashFor  = 3 * time.Second

	confluenceLabel = "Confluence"
)

var (
	issueKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]+-[0-9]+$`)
	prURL    = regexp.MustCompile(`^/[^/]+/[^/]+/pull/([0-9]+)`)
)

// link is one entry of a card's links: frontmatter.
type link struct {
	label string // what the detail shows: EMI-301, PR #812
	url   string
}

// issueBase is the URL an issue key's page starts with, from TRACKS_ISSUE_URLS: comma-separated
// PREFIX=URL pairs, where PREFIX is the key's project (EMI) or * for every other project, such as
// EMI=https://linear.app/you/issue/,*=https://you.atlassian.net/browse/. "" when none fits.
func issueBase(key string) string {
	project, _, _ := strings.Cut(key, "-")
	fallback := ""
	for _, f := range envList("TRACKS_ISSUE_URLS") {
		k, v, ok := strings.Cut(f, "=")
		k, v = strings.ToUpper(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch {
		case !ok || v == "":
		case k == project:
			return v
		case k == "*":
			fallback = v
		}
	}
	return fallback
}

// parseLinks reads the comma-separated links: value. Issue keys map to the tracker
// TRACKS_ISSUE_URLS names for their project; http(s) URLs pass through; anything else is dropped.
func parseLinks(v string) []link {
	var out []link
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		switch l, ok := toLink(f); {
		case !ok:
		case !hasLink(out, l.url):
			out = append(out, l)
		}
	}
	return out
}

func hasLink(ls []link, u string) bool {
	for _, l := range ls {
		if l.url == u {
			return true
		}
	}
	return false
}

func toLink(f string) (link, bool) {
	if issueKey.MatchString(f) {
		key := strings.ToUpper(f)
		base := issueBase(key)
		if base == "" {
			return link{}, false
		}
		return link{key, base + key}, true
	}
	if strings.IndexFunc(f, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return link{}, false
	}
	u, err := url.Parse(f)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return link{}, false
	}
	label := u.Hostname()
	if m := prURL.FindStringSubmatch(u.EscapedPath()); m != nil && u.Hostname() == "github.com" {
		label = "PR #" + m[1]
	}
	if strings.HasSuffix(u.Hostname(), ".atlassian.net") && strings.HasPrefix(u.EscapedPath(), "/wiki/") {
		label = confluenceLabel
	}
	label = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, label)
	return link{label, f}, true
}

func (c *Card) links() []link { return parseLinks(c.Meta["links"]) }

// openURL hands the URL to macOS open as one argv entry, never through a shell.
var openURL = func(u string) error {
	cmd := exec.Command("open", u)
	cmd.WaitDelay = waitDelay
	return cmd.Run()
}

type noteMsg string

func openLinkCmd(l link) tea.Cmd {
	return func() tea.Msg {
		if err := openURL(l.url); err != nil {
			return noteMsg("could not open " + l.label + ": " + err.Error())
		}
		return noteMsg("Opened " + l.label)
	}
}

// pickLink is the link o (cycle false: the first, or the next within linkCycle of the last o)
// or O (cycle true: always the next) opens. last is the index opened before, -1 for none.
func pickLink(n, last int, since time.Duration, cycle bool) int {
	switch {
	case n == 0:
		return -1
	case last < 0 || !(cycle || since <= linkCycle):
		return 0
	}
	return (last + 1) % n
}

func (m *paneModel) openLink(c *Card, cycle bool) tea.Cmd {
	ls := c.links()
	if len(ls) == 0 {
		m.flashMsg("No links on " + c.Name + ": add links: to its card")
		return nil
	}
	last := -1
	if m.linkTrack == c.Name {
		last = m.linkIdx
	}
	i := pickLink(len(ls), last, time.Since(m.linkAt), cycle)
	m.linkTrack, m.linkIdx, m.linkAt = c.Name, i, time.Now()
	return openLinkCmd(ls[i])
}

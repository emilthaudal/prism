package store

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Attachment kinds.
const (
	KindImage = "image"
	KindPage  = "page"
	KindFile  = "file"
)

// Attachment is a picture, page or file an ask points at. URL is http(s) or file.
type Attachment struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}

func nonNilAtt(a []Attachment) []Attachment {
	if a == nil {
		return []Attachment{}
	}
	return a
}

var (
	// seeRef is "(see: X)" or a bare "See: X"; X is checked by attachmentOf, and a match that is not a
	// path or URL stays in the text.
	seeRef    = regexp.MustCompile(`(?i)\(\s*see:\s*([^()]+?)\s*\)|\bsee:\s*([^\s()]+)`)
	holder    = regexp.MustCompile("([\x00\x02])(\\d+)\x01")
	imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true, ".heic": true}
	pageExts  = map[string]bool{".html": true, ".htm": true}
)

// takeRefs replaces each "See: X" whose X is an absolute or ~ path or an http(s) URL with a
// placeholder (\x00 for the parenthesised form, \x02 for the bare one), and returns the attachments
// the placeholders stand for.
func takeRefs(text string) (string, []Attachment) {
	var atts []Attachment
	out := seeRef.ReplaceAllStringFunc(text, func(m string) string {
		sub := seeRef.FindStringSubmatch(m)
		target := sub[1]
		if target == "" {
			target = strings.TrimRight(sub[2], ".,;:!?")
		}
		a, ok := attachmentOf(target)
		if !ok {
			return m
		}
		atts = append(atts, a)
		mark := "\x02"
		if sub[1] != "" {
			mark = "\x00"
		}
		return mark + strconv.Itoa(len(atts)-1) + "\x01"
	})
	return out, atts
}

// resolveRefs removes the placeholders from s and returns the attachments they stood for, the
// parenthesised ones apart from the bare ones.
func resolveRefs(s string, refs []Attachment) (clean string, paren, bare []Attachment) {
	s = holder.ReplaceAllStringFunc(s, func(m string) string {
		sub := holder.FindStringSubmatch(m)
		if n, err := strconv.Atoi(sub[2]); err == nil && n < len(refs) {
			if sub[1] == "\x00" {
				paren = append(paren, refs[n])
			} else {
				bare = append(bare, refs[n])
			}
		}
		return " "
	})
	return strings.Join(strings.Fields(s), " "), paren, bare
}

func attachmentOf(target string) (Attachment, bool) {
	switch {
	case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
		u, err := url.Parse(target)
		if err != nil || u.Host == "" {
			return Attachment{}, false
		}
		kind := KindPage
		if imageExts[strings.ToLower(path.Ext(u.Path))] {
			kind = KindImage
		}
		return Attachment{Label: u.Host, URL: target, Kind: kind}, true
	case strings.HasPrefix(target, "file://"):
		u, err := url.Parse(target)
		if err != nil || u.Path == "" {
			return Attachment{}, false
		}
		return fileAttachment(u.Path), true
	case strings.HasPrefix(target, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return Attachment{}, false
		}
		return fileAttachment(filepath.Join(home, target[2:])), true
	case strings.HasPrefix(target, "/"):
		return fileAttachment(filepath.Clean(target)), true
	}
	return Attachment{}, false
}

func fileAttachment(p string) Attachment {
	kind := KindFile
	switch ext := strings.ToLower(filepath.Ext(p)); {
	case imageExts[ext]:
		kind = KindImage
	case pageExts[ext]:
		kind = KindPage
	}
	return Attachment{Label: filepath.Base(p), URL: (&url.URL{Scheme: "file", Path: p}).String(), Kind: kind}
}

const (
	minProseItems = 3
	maxProseItems = 9
	maxItemWords  = 6
	rightQuote    = 0x2019
)

var orWord = regexp.MustCompile(`(?i)(?:^|\s)or\s+`)

// proseOptions reads the one shape of prose list it trusts: the question's last sentence has a
// colon, then 3 to 9 short items separated by commas with a final "or" (or exactly two items
// joined by "or"), and the sentence ends in "?". Anything else gives no options.
func proseOptions(q string) []QOption {
	body, ok := strings.CutSuffix(strings.TrimSpace(q), "?")
	if !ok {
		return nil
	}
	for _, end := range []string{". ", "! ", "? "} {
		if i := strings.LastIndex(body, end); i >= 0 {
			body = body[i+len(end):]
		}
	}
	colon := strings.LastIndex(body, ": ")
	if colon < 1 || strings.TrimSpace(body[:colon]) == "" {
		return nil
	}
	parts := strings.Split(body[colon+2:], ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	last := parts[len(parts)-1]
	locs := orWord.FindAllStringIndex(last, -1)
	if len(locs) == 0 {
		return nil
	}
	cut := locs[len(locs)-1]
	items := append(parts[:len(parts)-1:len(parts)-1], strings.TrimSpace(last[:cut[0]]), strings.TrimSpace(last[cut[1]:]))
	if items[len(items)-2] == "" {
		items = append(items[:len(items)-2], items[len(items)-1])
	}
	switch n := len(items); {
	case n == 2 && len(parts) == 1:
	case n >= minProseItems && n <= maxProseItems:
	default:
		return nil
	}
	out := make([]QOption, 0, len(items))
	for _, it := range items {
		if !proseItem(it) {
			return nil
		}
		out = append(out, QOption{Label: it})
	}
	return out
}

func proseItem(s string) bool {
	if n := len(strings.Fields(s)); n < 1 || n > maxItemWords || orWord.MatchString(s) {
		return false
	}
	letter := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r), r == ' ', r == '-', r == '\'', r == rightQuote, r == '&', r == '+':
		default:
			return false
		}
	}
	return letter
}

// pickedByLabel is the option whose label the recommendation starts with, case-insensitively and
// at a word end; the longest label wins.
func pickedByLabel(opts []QOption, rec string) int {
	r := strings.ToLower(strings.TrimSpace(rec))
	best, bestLen := 0, 0
	for i, o := range opts {
		l := strings.ToLower(o.Label)
		rest, ok := strings.CutPrefix(r, l)
		if !ok || len(l) <= bestLen {
			continue
		}
		if rest != "" {
			if c := []rune(rest)[0]; unicode.IsLetter(c) || unicode.IsDigit(c) {
				continue
			}
		}
		best, bestLen = i+1, len(l)
	}
	return best
}

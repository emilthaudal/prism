package tracks

import (
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// minTopicWords is how many topic words a request needs for its filler words to be left out.
const minTopicWords = 2

var (
	// stopwords are never part of a name.
	stopwords = wordSet(`
		i im ive id me my mine we our ours us lets you your can could would should shall will
		think guess want need like please maybe just really also so then
		still very quite pretty too rather actually basically honest honestly kind sort bit
		good bad great nice fine okay ok
		the a an this that these those some any it its is are was were be been am do does did
		to of for and or but in on at by with about from into onto as if
		how what whats why when where which who whose has have had having not there own
		ill youll weve theyre they them their one ones lot lots people through
		often even though always sometimes never everywhere
		jeg vi vores du din dit mig os det den de en et at og eller til om med af for er
		skal kan vil gerne lige bare maaske paa hvordan hvad hvorfor hvornaar hvor hvilke som der har ikke`)
	// weakWords say how Emil asks rather than what about; a name keeps them only when the
	// request has too few topic words.
	weakWords = wordSet(`
		see look looking check make made take try get find figure out here
		learnings learning lessons takeaways insights things thing stuff ideas
		team thinks thought way ways more much something anything
		improve improving better		read article post blog page link
		saw seen noticed trying tried go going got getting give prep list usage left week work
		linear jira ticket task issue track screenshot screenshots
		kigge kig se lave finde ud ting noget nogle laering laeringer`)
	// genericSegments are URL path segments that never say what a page is about.
	genericSegments = wordSet(`index default home main master latest blob tree edit view wiki
		en en-us page pages post posts blog article articles amp browse issue issues
		d document documents spreadsheets presentation file folders drive u 0 1 2 jira software projects`)
	// markerSegments say what kind of page follows; what comes before them is an owner or a
	// container (a user, a repo, a project), not the topic, so the search stops there.
	markerSegments = wordSet(`status statuses runs run commit commits pull pulls watch item
		boards board logs log dashboard dashboards`)
	// genericHosts are host labels that say nothing about the site.
	genericHosts = wordSet(`www app m`)
	webExts      = wordSet(`.html .htm .php .asp .aspx .jsp .md .pdf`)
	urlRe        = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"]*`)
	// localPathRe is a local file path such as a dropped screenshot: it starts a word with / or
	// ~/, has a second /, and may hold backslash-escaped spaces.
	localPathRe = regexp.MustCompile(`(?:^|[\s("'])((?:~/|/)(?:\\.|[^\s\\"'])*/(?:\\.|[^\s\\"'])*)`)
	// scratchPath marks a path whose name says nothing: a temporary file or a screenshot.
	scratchPath   = regexp.MustCompile(`(?i)^/(?:var/folders|tmp|private)/|/TemporaryItems/|/(?:screenshot|screen shot|cleanshot)[^/]*$`)
	uuidRe        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	minOpaqueLen  = 16 // ids are at least this long
	minIDSwitches = 3  // and switch between letters and digits at least this often
)

// translit spells the Latin letters Emil types most as ASCII; a word with any other
// non-ASCII letter is left out of the name rather than mangled.
var translit = map[rune]string{
	'æ': "ae", 'ø': "oe", 'å': "aa", 'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss",
	'é': "e", 'è': "e", 'ê': "e", 'á': "a", 'à': "a", 'â': "a", 'ó': "o", 'ò': "o",
	'ô': "o", 'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ë': "e", 'ú': "u", 'ù': "u", 'û': "u", 'ñ': "n", 'ç': "c",
}

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func allOf(s string, ok func(rune) bool) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !ok(r) }) < 0
}

func isDigit(r rune) bool  { return r >= '0' && r <= '9' }
func isLetter(r rune) bool { return r >= 'a' && r <= 'z' }

// isKey is whether a word is a ticket key such as pan-213.
func isKey(w string) bool {
	k, n, ok := strings.Cut(w, "-")
	return ok && len(k) >= 2 && allOf(k, isLetter) && allOf(n, isDigit)
}

// slugWords are the words of plain text in lowercase ASCII. A ticket key such as PAN-213
// stays one word.
func slugWords(s string) []string {
	var words []string
	var w strings.Builder
	foreign, glue := false, false
	end := func(sep rune) {
		word, ok := w.String(), w.Len() > 0 && !foreign
		w.Reset()
		foreign = false
		if ok {
			if n := len(words); glue && allOf(word, isDigit) && len(words[n-1]) <= 10 && allOf(words[n-1], isLetter) {
				words[n-1] += "-" + word
			} else {
				words = append(words, word)
			}
		}
		glue = ok && sep == '-'
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case isLetter(r) || isDigit(r):
			w.WriteRune(r)
		case translit[r] != "":
			w.WriteString(translit[r])
		case r == '\'' || r == '’':
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			foreign = true
		default:
			end(r)
		}
	}
	end(0)
	return words
}

// idSegment is a lowercase path segment that is a number, a date, a hash, a UUID or an
// opaque id such as a Google Docs one.
func idSegment(s string) bool {
	if allOf(strings.ReplaceAll(s, "-", ""), isDigit) {
		return true
	}
	if uuidRe.MatchString(s) {
		return true
	}
	if len(s) >= 7 && strings.ContainsFunc(s, isDigit) && allOf(s, func(r rune) bool { return isDigit(r) || r >= 'a' && r <= 'f' }) {
		return true
	}
	return slices.ContainsFunc(segParts(s), opaquePart)
}

func segParts(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune("-_+.", r) })
}

// opaquePart is a word too long and too mixed with digits to be one, like a Google Docs id;
// postgres16replication switches between letters and digits only twice, so it is a word.
func opaquePart(p string) bool {
	if len(p) < minOpaqueLen {
		return false
	}
	switches := 0
	for i := range len(p) {
		c := p[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
		if i > 0 && isDigit(rune(c)) != isDigit(rune(p[i-1])) {
			switches++
		}
	}
	return switches >= minIDSwitches
}

// trimIDTail drops a trailing id from a title segment, as Notion writes Title-Words-<id>.
func trimIDTail(seg string) string {
	i := strings.LastIndexAny(seg, "-_")
	if i > 0 && opaquePart(seg[i+1:]) {
		return seg[:i]
	}
	return seg
}

// topicWords are the words of the last meaningful segment of a path, after any ticket key
// earlier in it. stripExt drops any file extension, for a source file's path.
func topicWords(raws []string, stripExt bool) []string {
	for i := len(raws) - 1; i >= 0; i-- {
		seg := strings.ToLower(trimIDTail(raws[i]))
		if ext := path.Ext(seg); webExts[ext] || stripExt && ext != "" {
			seg = strings.TrimSuffix(seg, ext)
		}
		if markerSegments[seg] {
			return nil
		}
		if genericSegments[seg] || idSegment(seg) {
			continue
		}
		words := slugWords(seg)
		if !slices.ContainsFunc(words, func(w string) bool { return !stopwords[w] }) {
			continue
		}
		// A one-word kind followed by a number names one thing, as monitors/168516826 does.
		if len(words) == 1 && i+1 < len(raws) && allOf(raws[i+1], isDigit) {
			words[0] += "-" + raws[i+1]
		}
		var keys []string
		for _, s := range raws[:i] {
			if w := slugWords(s); len(w) == 1 && isKey(w[0]) {
				keys = append(keys, w[0])
			}
		}
		return append(keys, words...)
	}
	return nil
}

// urlWords are what a link says the work is about: a GitHub pull request or issue gives its
// repo and number, a file or wiki page its repo and topicWords, any other GitHub link its
// repo, any other URL its topicWords. Scheme, host, query and fragment never count. host is
// the host's first telling label, for when nothing else names the request.
func urlWords(raw string) (words []string, host string) {
	raw = strings.TrimRight(raw, ".,;:!?)]}'\"")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ""
	}
	for _, l := range strings.Split(strings.ToLower(u.Hostname()), ".") {
		if !genericHosts[l] {
			if w := slugWords(l); len(w) == 1 {
				host = w[0]
			}
			break
		}
	}
	raws := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	segs := strings.FieldsFunc(strings.ToLower(u.Path), func(r rune) bool { return r == '/' })
	if strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") == "github.com" && len(segs) >= 2 {
		repo := strings.Join(slugWords(segs[1]), "-")
		if len(segs) >= 4 && (segs[2] == "pull" || segs[2] == "issues") && allOf(segs[3], isDigit) {
			kind := map[string]string{"pull": "pr", "issues": "issue"}[segs[2]]
			if repo == "" {
				return []string{kind + "-" + segs[3]}, host
			}
			return []string{repo, kind + "-" + segs[3]}, host
		}
		var rest []string
		switch {
		case len(segs) >= 5 && segs[2] == "blob": // blob/BRANCH/path/to/file
			rest = topicWords(raws[4:], true)
		case len(segs) >= 4 && segs[2] == "wiki":
			rest = topicWords(raws[3:], false)
		}
		if repo != "" {
			return append([]string{repo}, rest...), host
		}
	}
	if strings.HasSuffix(strings.ToLower(u.Hostname()), ".google.com") {
		// Docs, Sheets, Slides and Drive put the file id right after /d/. Ids are base64url,
		// so they often hold - or _ and would otherwise read as words.
		for i := 0; i+1 < len(segs); i++ {
			if segs[i] == "d" {
				raws = append(raws[:i+1:i+1], raws[i+2:]...)
				break
			}
		}
	}
	return topicWords(raws, false), host
}

// requestWords are a request's words in order, each link replaced by its urlWords, the
// links' hosts, and the ticket keys typed as text rather than inside a link.
func requestWords(req string) (words, hosts []string, keys map[string]bool) {
	keys = map[string]bool{}
	text := func(s string) {
		for _, w := range slugWords(s) {
			if isKey(w) {
				keys[w] = true
			}
			words = append(words, w)
		}
	}
	type span struct {
		from, to int
		path     bool
	}
	var spans []span
	for _, loc := range urlRe.FindAllStringIndex(req, -1) {
		spans = append(spans, span{loc[0], loc[1], false})
	}
	for _, loc := range localPathRe.FindAllStringSubmatchIndex(req, -1) {
		spans = append(spans, span{loc[2], loc[3], true})
	}
	slices.SortFunc(spans, func(a, b span) int { return a.from - b.from })
	at := 0
	for _, s := range spans {
		if s.from < at {
			continue
		}
		text(req[at:s.from])
		if s.path {
			words = append(words, pathWords(req[s.from:s.to])...)
		} else {
			w, h := urlWords(req[s.from:s.to])
			words = append(words, w...)
			if h != "" {
				hosts = append(hosts, h)
			}
		}
		at = s.to
	}
	text(req[at:])
	return words, hosts, keys
}

// pathWords are what a local file path says the work is about: its file name, or nothing
// for a temporary file or a screenshot.
func pathWords(p string) []string {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, ""), ".,;:!?)]}")
	if scratchPath.MatchString(p) {
		return nil
	}
	return topicWords(strings.FieldsFunc(p, func(r rune) bool { return r == '/' }), true)
}

// slugName is the name a request reads as: its ticket keys, then its first topic words,
// else its first link's host, else its filler words, else track-HHMM. Filler words count
// only when there are too few topic words.
func slugName(req string, now time.Time) string {
	var cands []string
	seen, topic := map[string]bool{}, 0
	words, hosts, keys := requestWords(req)
	for _, w := range words {
		if stopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		cands = append(cands, w)
		switch {
		case keys[w]:
			topic += minTopicWords // a ticket key names the work on its own
		case !weakWords[w]:
			topic++
		}
	}
	if topic == 0 && len(hosts) > 0 {
		cands = hosts[:1]
	}
	// A ticket key typed in the request leads the name; one inside a link keeps its place.
	slices.SortStableFunc(cands, func(a, b string) int {
		ka, kb := keys[a], keys[b]
		switch {
		case ka && !kb:
			return -1
		case kb && !ka:
			return 1
		}
		return 0
	})
	var parts []string
	n := 0
	for _, w := range cands {
		if weakWords[w] && topic >= minTopicWords {
			continue
		}
		if len(parts) == maxSlugWords || (n > 0 && n+1+len(w) > maxSlugLen) {
			break
		}
		if n > 0 {
			n++
		}
		parts, n = append(parts, w), n+len(w)
	}
	if len(parts) == 0 {
		return "track-" + now.Local().Format("1504")
	}
	s := strings.Join(parts, "-")
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	return s
}

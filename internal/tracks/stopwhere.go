package tracks

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
)

// stopWhereKey holds a hash of the first line of Where it is as the Stop hook last saw it. A
// different line at the next Stop means the agent wrote the card during the turn.
const stopWhereKey = "stop_where"

// whereMax is the longest first line of Where it is the hook writes from a report.
const whereMax = 200

// stopWhere keeps Where it is current without the agent: when the agent left the card alone this
// turn, the first line of its report becomes the card's first line. The first Stop on a card
// only records what it finds there.
func stopWhere(c *Card, report string) error {
	cur := whereFirstLine(c)
	if c.Meta[stopWhereKey] != hashLine(cur) {
		return setMetas(c.Name, [][2]string{{stopWhereKey, hashLine(cur)}})
	}
	line := reportLine(report)
	if line == "" || line == cur {
		return nil
	}
	in := "where: " + line + "\n" + strings.Join(whereDetail(c), "\n") + "\n"
	if err := cardCmd([]string{c.Name}, strings.NewReader(in), io.Discard); err != nil {
		return err
	}
	return setMetas(c.Name, [][2]string{{stopWhereKey, hashLine(line)}})
}

func whereTextLines(c *Card) []string {
	var out []string
	for _, l := range strings.Split(c.Sections["Where it is"], "\n") {
		if l = strings.TrimSpace(l); l != "" && !tierLine.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

func whereFirstLine(c *Card) string {
	if ls := whereTextLines(c); len(ls) > 0 {
		return ls[0]
	}
	return ""
}

// whereDetail is the card's detail lines under the first where: line, which the hook keeps.
func whereDetail(c *Card) []string {
	if ls := whereTextLines(c); len(ls) > 1 {
		return ls[1:]
	}
	return nil
}

// reportLine is the first line of the agent's report as one card line, or "" when it has none
// the card can hold.
func reportLine(report string) string {
	for _, l := range strings.Split(cleanText(report), "\n") {
		l = strings.Join(strings.Fields(strings.TrimLeft(strings.TrimSpace(l), "#*->  ")), " ")
		l = strings.ReplaceAll(l, " # ", " ")
		if l != "" {
			return clipRunes(l, whereMax)
		}
	}
	return ""
}

func hashLine(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

package tracks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/deskui"
)

// usagePath is where Emil's status line script leaves the Claude usage limits Claude Code hands it:
// {"at": "2026-11-25T10:40:00Z", "five_hour": {"used_percentage": 42, "resets_at": …}, "seven_day": {…}}.
// The script writes it at most once a minute; nothing in tracks writes it.
func usagePath() string { return filepath.Join(home(), "usage.json") }

// usageStamp changes when usage.json does, so serve sends the desk again.
func usageStamp() string {
	fi, err := os.Stat(usagePath())
	if err != nil {
		return "usage:none"
	}
	return fmt.Sprintf("usage:%d:%d", fi.ModTime().UnixNano(), fi.Size())
}

// readUsage is usage.json for the snapshot, or nil when it is missing or unreadable.
func readUsage() *deskui.Usage {
	b, err := os.ReadFile(usagePath())
	if err != nil {
		return nil
	}
	var raw struct {
		At       string          `json:"at"`
		FiveHour json.RawMessage `json:"five_hour"`
		SevenDay json.RawMessage `json:"seven_day"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	u := &deskui.Usage{FiveHour: usageLimit(raw.FiveHour), SevenDay: usageLimit(raw.SevenDay)}
	if u.FiveHour == nil && u.SevenDay == nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw.At); err == nil {
		u.UpdatedAt = apiTime(t)
	} else if fi, err := os.Stat(usagePath()); err == nil {
		u.UpdatedAt = apiTime(fi.ModTime())
	}
	return u
}

// usageLimit reads one limit as Claude Code gives it: used_percentage, and the reset as resets_at or
// reset_at, in Unix seconds or RFC 3339.
func usageLimit(raw json.RawMessage) *deskui.UsageLimit {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v struct {
		Used     *float64        `json:"used_percentage"`
		ResetsAt json.RawMessage `json:"resets_at"`
		ResetAt  json.RawMessage `json:"reset_at"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Used == nil {
		return nil
	}
	l := &deskui.UsageLimit{UsedPercentage: *v.Used}
	r := v.ResetsAt
	if len(r) == 0 || string(r) == "null" {
		r = v.ResetAt
	}
	if t := resetTime(r); !t.IsZero() {
		l.ResetsAt = &t
	}
	return l
}

func resetTime(raw json.RawMessage) time.Time {
	s := strings.Trim(string(raw), `"`)
	if s == "" || s == "null" {
		return time.Time{}
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n > 1e12 { // milliseconds
			n /= 1000
		}
		return apiTime(time.Unix(int64(n), 0))
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return apiTime(t)
	}
	return time.Time{}
}

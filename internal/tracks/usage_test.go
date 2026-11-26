package tracks

import (
	"os"
	"testing"
	"time"
)

func TestReadUsage(t *testing.T) {
	t.Setenv("TRACKS_HOME", t.TempDir())
	if u := readUsage(); u != nil {
		t.Fatalf("no file: got %+v, want nil", u)
	}
	before := usageStamp()

	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(usagePath(), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"at":"2026-11-25T10:40:00Z","five_hour":{"used_percentage":42.5,"resets_at":1795606800},"seven_day":{"used_percentage":18,"reset_at":"2026-11-30T08:00:00Z"}}`)
	if usageStamp() == before {
		t.Fatal("stamp did not change when usage.json appeared")
	}
	u := readUsage()
	if u == nil || u.FiveHour == nil || u.SevenDay == nil {
		t.Fatalf("got %+v", u)
	}
	if u.FiveHour.UsedPercentage != 42.5 || !u.FiveHour.ResetsAt.Equal(time.Unix(1795606800, 0)) {
		t.Errorf("five_hour = %+v", u.FiveHour)
	}
	if u.SevenDay.UsedPercentage != 18 || !u.SevenDay.ResetsAt.Equal(time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("seven_day = %+v", u.SevenDay)
	}
	if !u.UpdatedAt.Equal(time.Date(2026, 11, 25, 10, 40, 0, 0, time.UTC)) {
		t.Errorf("updated_at = %v", u.UpdatedAt)
	}

	write(`{"five_hour":{"used_percentage":7}}`)
	u = readUsage()
	if u == nil || u.FiveHour == nil || u.SevenDay != nil || u.FiveHour.ResetsAt != nil || u.UpdatedAt.IsZero() {
		t.Errorf("partial: got %+v", u)
	}

	for _, bad := range []string{`not json`, `{}`, `{"five_hour":{"resets_at":1}}`} {
		write(bad)
		if u := readUsage(); u != nil {
			t.Errorf("%s: got %+v, want nil", bad, u)
		}
	}
}

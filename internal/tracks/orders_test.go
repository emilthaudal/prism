package tracks

import (
	"strings"
	"testing"
)

func TestOrdersAddListDropAndBindPrintsThem(t *testing.T) {
	decisionHome(t, "alpha")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv(deskTokenEnv, "")
	if out, _ := do("orders"); out != "no standing orders\n" {
		t.Fatalf("empty: %q", out)
	}
	if out, code := do("bind", "alpha"); code != 0 || strings.Contains(out, "Orders:") {
		t.Fatalf("bind with no orders: %d %q", code, out)
	}
	if out, code := do("orders", "add", "no install until  sessions persist"); code != 0 || out != "O1\n" {
		t.Fatalf("add: %d %q", code, out)
	}
	do("orders", "add", "the desk merges tracks branches into main")
	if out, code := do("orders", "add", "  "); code == 0 {
		t.Errorf("empty order accepted: %q", out)
	}
	if out, _ := do("orders"); out != "O1  no install until sessions persist\nO2  the desk merges tracks branches into main\n" {
		t.Errorf("list: %q", out)
	}
	out, _ := do("bind", "alpha")
	if !strings.Contains(out, "\nOrders: ") || !strings.Contains(out, "- O1 no install until sessions persist\n- O2 the desk merges") {
		t.Errorf("bind does not print the orders:\n%s", out)
	}
	if got := withOrders("Also check b2b."); !strings.HasPrefix(got, "Also check b2b.\n\nOrders: ") || !strings.Contains(got, "- O2 ") {
		t.Errorf("withOrders: %q", got)
	}
	if out, code := do("orders", "drop", "O1"); code != 0 || out != "dropped O1\n" {
		t.Fatalf("drop: %d %q", code, out)
	}
	if _, code := do("orders", "drop", "O1"); code == 0 {
		t.Error("dropped twice")
	}
	if out, _ := do("orders", "--json"); !strings.Contains(out, `"id": "O2"`) || strings.Contains(out, "O1") {
		t.Errorf("json: %s", out)
	}
}

func TestATrackAgentCannotChangeOrders(t *testing.T) {
	decisionHome(t, "alpha")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0b6c3f4e-1d2a-4c3b-9e8f-123456789abc")
	t.Setenv(deskTokenEnv, "")
	for _, args := range [][]string{{"orders", "add", "merge everything"}, {"orders", "drop", "1"}} {
		if out, code := do(args...); code == 0 || !strings.Contains(out, "refused") {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
	if out, code := do("orders"); code != 0 || out != "no standing orders\n" {
		t.Errorf("an agent can't read them: %d %q", code, out)
	}
}

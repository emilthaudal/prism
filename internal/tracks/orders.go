package tracks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/emilthaudal/prism/internal/store"
)

const ordersUsage = "usage: tracks orders [--json] | tracks orders add 'text' | tracks orders drop N"

// ordersBlock is the standing orders as a session reads them after its card (tracks bind) and
// under each note the desk sends it, "" when there are none.
func ordersBlock(orders []store.Order) string {
	if len(orders) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nOrders: Emil's standing orders, for every session (tracks orders lists them):\n")
	for _, o := range orders {
		fmt.Fprintf(&b, "- O%d %s\n", o.ID, o.Text)
	}
	return b.String()
}

// withOrders is text with the standing orders in force under it.
func withOrders(text string) string {
	var orders []store.Order
	if err := withBareStore(func(st *store.Store) (err error) { orders, err = st.Orders(); return err }); err != nil {
		return text
	}
	if block := ordersBlock(orders); block != "" {
		return strings.TrimRight(text, "\n") + "\n" + block
	}
	return text
}

// ordersWriteRefusal says why this process may not change the standing orders, or "". Emil
// changes them from a terminal, the desk from its session; a track's agent only reads them,
// since an order it wrote would reach every other session.
func ordersWriteRefusal() string {
	if os.Getenv("CLAUDE_CODE_SESSION_ID") == "" && os.Getenv(deskTokenEnv) == "" {
		return ""
	}
	deskSess, err := deskSessionNow()
	if err != nil {
		return err.Error()
	}
	if why := deskCallerRefusal(deskSess, callerSession()); why != "" {
		return "only Emil or the desk changes standing orders: " + why
	}
	return ""
}

type orderJSON struct {
	ID        string `json:"id"`
	N         int64  `json:"n"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

func ordersCmd(args []string, out io.Writer) error {
	if !storeOn() {
		return errors.New("tracks orders " + needsStore)
	}
	switch {
	case len(args) == 0 || len(args) == 1 && args[0] == "--json":
		var orders []store.Order
		if err := withBareStore(func(st *store.Store) (err error) { orders, err = st.Orders(); return err }); err != nil {
			return err
		}
		if len(args) == 1 {
			doc := []orderJSON{}
			for _, o := range orders {
				doc = append(doc, orderJSON{orderLabel(o.ID), o.ID, o.Text, o.CreatedAt.UTC().Format(time.RFC3339)})
			}
			b, err := json.MarshalIndent(map[string]any{"orders": doc}, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s\n", b)
			return err
		}
		if len(orders) == 0 {
			fmt.Fprintln(out, "no standing orders")
		}
		for _, o := range orders {
			fmt.Fprintf(out, "%s  %s\n", orderLabel(o.ID), o.Text)
		}
		return nil
	case len(args) == 2 && args[0] == "add":
		if why := ordersWriteRefusal(); why != "" {
			return errors.New("refused: " + why)
		}
		var o store.Order
		if err := withBareStore(func(st *store.Store) (err error) { o, err = st.AddOrder(args[1]); return err }); err != nil {
			return fmt.Errorf("refused: %w", err)
		}
		fmt.Fprintln(out, orderLabel(o.ID))
		return nil
	case len(args) == 2 && args[0] == "drop":
		if why := ordersWriteRefusal(); why != "" {
			return errors.New("refused: " + why)
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(args[1], "O"), "o"), 10, 64)
		if err != nil {
			return errors.New(ordersUsage)
		}
		if err := withBareStore(func(st *store.Store) error { return st.DropOrder(id) }); err != nil {
			return fmt.Errorf("refused: %w", err)
		}
		fmt.Fprintf(out, "dropped %s\n", orderLabel(id))
		return nil
	}
	return errors.New(ordersUsage)
}

func orderLabel(id int64) string { return "O" + strconv.FormatInt(id, 10) }

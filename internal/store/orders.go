package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ordersSchema is the schema that added desk_orders.
const ordersSchema = 15

// Standing orders are what Emil told the desk to hold for every session: "no install until
// sessions persist", "the desk merges tracks main". They live here rather than in the desk's
// memory, so the sessions the desk starts and sends to get them too.

var (
	ErrNoOrder    = errors.New("no such standing order")
	ErrEmptyOrder = errors.New("a standing order needs text")
)

// Order is one standing order, in force until dropped.
type Order struct {
	ID        int64
	Text      string
	CreatedAt time.Time
}

// AddOrder adds a standing order and returns it.
func (s *Store) AddOrder(text string) (Order, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return Order{}, ErrEmptyOrder
	}
	o := Order{Text: text, CreatedAt: s.now()}
	err := s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`INSERT INTO desk_orders (text, created_at) VALUES (?, ?)`, text, fmtTime(o.CreatedAt))
		if err != nil {
			return err
		}
		o.ID, err = res.LastInsertId()
		return err
	})
	return o, err
}

// DropOrder ends standing order id.
func (s *Store) DropOrder(id int64) error {
	return s.tx(func(tx *wtx) error {
		res, err := tx.Exec(`UPDATE desk_orders SET dropped_at = ? WHERE id = ? AND dropped_at IS NULL`, fmtTime(s.now()), id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, ErrNoOrder)
		}
		return nil
	})
}

// Orders are the standing orders in force, oldest first.
func (s *Store) Orders() ([]Order, error) {
	var out []Order
	err := s.read(func(q querier) error {
		rows, err := q.Query(`SELECT id, text, created_at FROM desk_orders WHERE dropped_at IS NULL ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o Order
			var at sql.NullString
			if err := rows.Scan(&o.ID, &o.Text, &at); err != nil {
				return err
			}
			if o.CreatedAt, err = parseTime(at); err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

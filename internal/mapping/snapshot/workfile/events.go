package workfile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// now is the clock for unit times; tests replace it.
var now = time.Now

// Save adds events to a unit in one transaction and records the unit, with
// finished marking it done. Events keep the order they are saved in.
func (f *File) Save(u snapshot.Unit, events []map[string]any, finished bool) (err error) {
	tx, err := f.db.Begin()
	if err != nil {
		return fmt.Errorf("save unit %s: %w", u.Key, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	item, ok := f.items[u.Key]
	if !ok {
		if item, err = seedItem(tx, u.Key, len(f.meta.Types)); err != nil {
			return fmt.Errorf("save unit %s: %w", u.Key, err)
		}
	}

	t := formatTime(now())
	var fin any
	if finished {
		fin = t
	}
	if _, err = tx.Exec(`INSERT INTO units(unit, ord, started_at, finished_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(unit) DO UPDATE SET ord = excluded.ord, finished_at = COALESCE(excluded.finished_at, finished_at)`,
		u.Key, u.Order, t, fin); err != nil {
		return fmt.Errorf("save unit %s: %w", u.Key, err)
	}

	stmt, err := tx.Prepare(`INSERT INTO events(unit, rank, ord, item, payload) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("save unit %s: %w", u.Key, err)
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range events {
		typ, _ := e["type"].(string)
		rank := slices.Index(f.meta.Types, typ)
		if rank < 0 {
			err = fmt.Errorf("save unit %s: event type %q is not one of the requested snapshot types", u.Key, typ)
			return err
		}
		var payload []byte
		if payload, err = json.Marshal(e); err != nil {
			return fmt.Errorf("save unit %s: encode event: %w", u.Key, err)
		}
		item++
		if _, err = stmt.Exec(u.Key, rank, u.Order, item, payload); err != nil {
			return fmt.Errorf("save unit %s: %w", u.Key, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("save unit %s: %w", u.Key, err)
	}
	f.items[u.Key] = item
	return nil
}

// UnitDone reports whether the unit was saved with finished set.
func (f *File) UnitDone(key string) bool {
	var n int
	err := f.db.QueryRow(`SELECT COUNT(*) FROM units WHERE unit = ? AND finished_at IS NOT NULL`, key).Scan(&n)
	return err == nil && n > 0
}

// DropUnfinished deletes the units that never finished, with their events, so
// a resumed run reads them again from the start.
func (f *File) DropUnfinished() (err error) {
	tx, err := f.db.Begin()
	if err != nil {
		return fmt.Errorf("drop unfinished units: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`DELETE FROM events WHERE unit IN (SELECT unit FROM units WHERE finished_at IS NULL)`); err != nil {
		return fmt.Errorf("drop unfinished units: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM units WHERE finished_at IS NULL`); err != nil {
		return fmt.Errorf("drop unfinished units: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("drop unfinished units: %w", err)
	}
	f.items = map[string]int{}
	return nil
}

// Remember saves a small value under key.
func (f *File) Remember(key, value string) error {
	if _, err := f.db.Exec(`INSERT INTO kv(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("remember %s: %w", key, err)
	}
	return nil
}

// Recall returns what Remember saved under key, or "" when there is none.
func (f *File) Recall(key string) string {
	var v string
	if err := f.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// Window returns the earliest time a unit started and the latest time one
// finished; zero when there is none.
func (f *File) Window() (started, finished time.Time) {
	rows, err := f.db.Query(`SELECT started_at, finished_at FROM units`)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s string
		var fin sql.NullString
		if rows.Scan(&s, &fin) != nil {
			continue
		}
		if t, err := parseTime(s); err == nil && !t.IsZero() && (started.IsZero() || t.Before(started)) {
			started = t
		}
		if fin.Valid {
			if t, err := parseTime(fin.String); err == nil && t.After(finished) {
				finished = t
			}
		}
	}
	return started, finished
}

// Replay gives every saved event to fn, page at a time, ordered by event
// type (as Meta.Types lists them), then organization, unit and the order
// saved in. Events get ids snap_000001, snap_000002, ... in that order. It
// returns how many events fn was given.
func (f *File) Replay(ctx context.Context, page int, fn func(events []map[string]any) error) (int, error) {
	if page < 1 {
		page = 1
	}
	var (
		rank, ord, item int
		unit            string
		first           = true
		total           int
	)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		q := `SELECT rank, ord, unit, item, payload FROM events `
		args := []any{}
		if !first {
			q += `WHERE (rank, ord, unit, item) > (?, ?, ?, ?) `
			args = append(args, rank, ord, unit, item)
		}
		q += `ORDER BY rank, ord, unit, item LIMIT ?`
		args = append(args, page)
		rows, err := f.db.QueryContext(ctx, q, args...)
		if err != nil {
			return total, fmt.Errorf("read events: %w", err)
		}
		batch := make([]map[string]any, 0, page)
		for rows.Next() {
			var payload []byte
			if err := rows.Scan(&rank, &ord, &unit, &item, &payload); err != nil {
				_ = rows.Close()
				return total, fmt.Errorf("read events: %w", err)
			}
			var e map[string]any
			if err := json.Unmarshal(payload, &e); err != nil {
				_ = rows.Close()
				return total, fmt.Errorf("decode event of %s: %w", unit, err)
			}
			e["id"] = fmt.Sprintf("snap_%06d", total+len(batch)+1)
			batch = append(batch, e)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return total, fmt.Errorf("read events: %w", err)
		}
		if len(batch) == 0 {
			return total, nil
		}
		first = false
		if err := fn(batch); err != nil {
			return total, err
		}
		total += len(batch)
	}
}

// seedItem returns the last item number saved for a unit by an earlier run, 0
// for a unit with no row. It reads the units row by key and then the unit's
// events by their primary key (rank, ord, unit), never scanning the table.
func seedItem(tx *sql.Tx, unit string, ranks int) (int, error) {
	var ord int
	err := tx.QueryRow(`SELECT ord FROM units WHERE unit = ?`, unit).Scan(&ord)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	last := 0
	for rank := range ranks {
		var m sql.NullInt64
		if err := tx.QueryRow(`SELECT MAX(item) FROM events WHERE rank = ? AND ord = ? AND unit = ?`,
			rank, ord, unit).Scan(&m); err != nil {
			return 0, err
		}
		last = max(last, int(m.Int64))
	}
	return last, nil
}

package workfile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// DiffKind says which way a diff row differs from the store.
type DiffKind int

const (
	Missing DiffKind = iota + 1
	Changed
	Stale
)

// Counts is how many rows each side of a Compare holds.
type Counts struct{ Desired, Missing, Changed, Stale int }

// pageRows is how many rows a keyset page reads before it calls back or
// inserts, so no query stays open while the single connection is used again.
const pageRows = 1000

// Keys in kv that hold the store read's mark.
const (
	kvStoreAt      = "store_read_at"
	kvStoreTotal   = "store_read_total"
	kvStoreObjects = "store_read_objects"
)

// ResetStore empties the store table and clears the store read's mark.
func (f *File) ResetStore(ctx context.Context) (err error) {
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reset store: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM store`); err != nil {
		return fmt.Errorf("reset store: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM kv WHERE key IN (?, ?, ?)`, kvStoreAt, kvStoreTotal, kvStoreObjects); err != nil {
		return fmt.Errorf("reset store: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("reset store: %w", err)
	}
	return nil
}

// ResetReplay empties what a replay builds (the planner's state, the tuple set,
// the desired tuples and the diff), so a resumed run replays the saved events
// from the start. The events, the units and the store read stay.
func (f *File) ResetReplay(ctx context.Context) (err error) {
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reset replay: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, stmt := range []string{`DELETE FROM plan`, `DELETE FROM tupleset`, `DELETE FROM desired`, `DELETE FROM diff`} {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("reset replay: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("reset replay: %w", err)
	}
	f.desired = nil
	return nil
}

// AddStore saves ts in the store table, in one transaction. A tuple read twice
// replaces itself.
func (f *File) AddStore(ctx context.Context, ts []language.Tuple) (err error) {
	if len(ts) == 0 {
		return nil
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save store tuples: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO store(id, tuple) VALUES(?, ?)`)
	if err != nil {
		return fmt.Errorf("save store tuples: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, t := range ts {
		blob, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("save store tuples: %w", err)
		}
		if _, err = stmt.ExecContext(ctx, snapshot.Identity(t), blob); err != nil {
			return fmt.Errorf("save store tuples: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("save store tuples: %w", err)
	}
	return nil
}

// MarkStoreRead saves when the store was read and how much it held. total
// counts crowded-pair re-reads, so it can exceed the rows in the store table.
func (f *File) MarkStoreRead(at time.Time, total, objects int) (err error) {
	tx, err := f.db.Begin()
	if err != nil {
		return fmt.Errorf("mark store read: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for k, v := range map[string]string{
		kvStoreAt:      at.UTC().Format(time.RFC3339Nano),
		kvStoreTotal:   strconv.Itoa(total),
		kvStoreObjects: strconv.Itoa(objects),
	} {
		if _, err = tx.Exec(`INSERT INTO kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return fmt.Errorf("mark store read: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("mark store read: %w", err)
	}
	return nil
}

// StoreRead returns what MarkStoreRead saved; ok is false when no read is
// marked.
func (f *File) StoreRead() (at time.Time, total, objects int, ok bool) {
	at, err := parseTime(f.Recall(kvStoreAt))
	if err != nil || at.IsZero() {
		return time.Time{}, 0, 0, false
	}
	total, err1 := strconv.Atoi(f.Recall(kvStoreTotal))
	objects, err2 := strconv.Atoi(f.Recall(kvStoreObjects))
	if err1 != nil || err2 != nil {
		return time.Time{}, 0, 0, false
	}
	return at, total, objects, true
}

// differs is the SQL that snapshot.Compare's comparison reduces to, for d
// (desired) and s (store): the condition name and the marshaled context, which
// Go leaves out when empty. Both blobs came from json.Marshal, which sorts map
// keys, so equal text means equal Key. The cast keeps SQLite from reading a
// BLOB as JSONB.
const differs = `(COALESCE(json_extract(CAST(d.tuple AS TEXT), '$.condition'), '') <> COALESCE(json_extract(CAST(s.tuple AS TEXT), '$.condition'), '')
OR COALESCE(json_extract(CAST(d.tuple AS TEXT), '$.context'), '') <> COALESCE(json_extract(CAST(s.tuple AS TEXT), '$.context'), ''))`

// Compare fills the diff table from desired against store, as snapshot.Compare
// does: Missing is a desired tuple the store lacks, Changed one whose condition
// or context differs, Stale a stored tuple desired lacks whose shape is in
// owned (none when owned is nil). Contested identities leave the store first.
// diff is cleared first, so Compare can run again.
func (f *File) Compare(ctx context.Context, owned []mapping.Shape, contested []snapshot.Contested) (c Counts, err error) {
	if err = f.FlushDesired(ctx); err != nil {
		return Counts{}, err
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return Counts{}, fmt.Errorf("compare: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			c = Counts{}
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM diff`); err != nil {
		return c, fmt.Errorf("compare: %w", err)
	}
	for _, ct := range contested {
		if _, err = tx.ExecContext(ctx, `DELETE FROM store WHERE id = ?`, snapshot.Identity(ct.Tuple)); err != nil {
			return c, fmt.Errorf("compare: %w", err)
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM desired`).Scan(&c.Desired); err != nil {
		return c, fmt.Errorf("compare: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO diff(kind, shape, user, relation, object, id, tuple, was)
SELECT ?, d.shape, d.user, d.relation, d.object, d.id, d.tuple, NULL FROM desired d
WHERE NOT EXISTS (SELECT 1 FROM store s WHERE s.id = d.id)`, int(Missing))
	if err != nil {
		return c, fmt.Errorf("compare: %w", err)
	}
	n, _ := res.RowsAffected()
	c.Missing = int(n)
	res, err = tx.ExecContext(ctx, `INSERT INTO diff(kind, shape, user, relation, object, id, tuple, was)
SELECT ?, d.shape, d.user, d.relation, d.object, d.id, d.tuple, s.tuple FROM desired d JOIN store s ON s.id = d.id
WHERE `+differs, int(Changed))
	if err != nil {
		return c, fmt.Errorf("compare: %w", err)
	}
	n, _ = res.RowsAffected()
	c.Changed = int(n)
	if len(owned) > 0 {
		if c.Stale, err = staleRows(ctx, tx, owned); err != nil {
			return c, fmt.Errorf("compare: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return c, fmt.Errorf("compare: %w", err)
	}
	return c, nil
}

// staleRows adds the stored tuples desired lacks and owned covers to diff, page
// by page, and returns how many it added.
func staleRows(ctx context.Context, tx *sql.Tx, owned []mapping.Shape) (int, error) {
	own := make(map[mapping.Shape]bool, len(owned))
	for _, s := range owned {
		own[s] = true
	}
	type row struct {
		id string
		t  language.Tuple
	}
	total, last := 0, ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT id, tuple FROM store
WHERE id > ? AND NOT EXISTS (SELECT 1 FROM desired d WHERE d.id = store.id) ORDER BY id LIMIT ?`, last, pageRows)
		if err != nil {
			return 0, err
		}
		var page []row
		for rows.Next() {
			var r row
			var blob []byte
			if err = rows.Scan(&r.id, &blob); err == nil {
				err = json.Unmarshal(blob, &r.t)
			}
			if err != nil {
				_ = rows.Close()
				return 0, err
			}
			page = append(page, r)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return 0, err
		}
		for _, r := range page {
			if !own[mapping.ShapeOf(r.t)] {
				continue
			}
			blob, err := json.Marshal(r.t)
			if err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO diff(kind, shape, user, relation, object, id, tuple, was) VALUES(?, ?, ?, ?, ?, ?, ?, NULL)`,
				int(Stale), snapshot.ShapeKey(r.t), r.t.User, r.t.Relation, r.t.Object, r.id, blob); err != nil {
				return 0, err
			}
			total++
		}
		if len(page) < pageRows {
			return total, nil
		}
		last = page[len(page)-1].id
	}
}

// DiffRows calls fn for each row of one kind in the diff table, in
// snapshot.Compare's order (shape, user, relation, object). was is the
// stored version for Changed and the zero tuple otherwise.
func (f *File) DiffRows(ctx context.Context, kind DiffKind, fn func(t, was language.Tuple) error) error {
	var last [4]string
	first := true
	for {
		q := `SELECT shape, user, relation, object, tuple, was FROM diff WHERE kind = ? `
		args := []any{int(kind)}
		if !first {
			q += `AND (shape, user, relation, object) > (?, ?, ?, ?) `
			args = append(args, last[0], last[1], last[2], last[3])
		}
		q += `ORDER BY shape, user, relation, object LIMIT ?`
		args = append(args, pageRows)
		rows, err := f.db.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		type pair struct{ t, was language.Tuple }
		var page []pair
		var keys [][4]string
		for rows.Next() {
			var k [4]string
			var blob, wasBlob []byte
			var p pair
			if err = rows.Scan(&k[0], &k[1], &k[2], &k[3], &blob, &wasBlob); err == nil {
				if err = json.Unmarshal(blob, &p.t); err == nil && wasBlob != nil {
					err = json.Unmarshal(wasBlob, &p.was)
				}
			}
			if err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, p)
			keys = append(keys, k)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, p := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fn(p.t, p.was); err != nil {
				return err
			}
		}
		if len(page) < pageRows {
			return nil
		}
		last, first = keys[len(keys)-1], false
	}
}

// wanted reads the desired table as what a store read looks for.
type wanted struct{ f *File }

// Wanted returns the desired table as what ReadMatchingFrom reads against.
func (f *File) Wanted() snapshot.Wanted { return wanted{f} }

func (w wanted) Pairs(ctx context.Context) ([]snapshot.Pair, error) {
	rows, err := w.f.db.QueryContext(ctx, `SELECT object, relation, COUNT(*) FROM desired GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	var out []snapshot.Pair
	for rows.Next() {
		var p snapshot.Pair
		if err = rows.Scan(&p.Object, &p.Relation, &p.N); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return out, nil
}

func (w wanted) Keys(ctx context.Context, object, relation string, fn func(language.Tuple) error) error {
	last := ""
	for {
		rows, err := w.f.db.QueryContext(ctx, `SELECT id, tuple FROM desired WHERE object = ? AND relation = ? AND id > ? ORDER BY id LIMIT ?`,
			object, relation, last, pageRows)
		if err != nil {
			return err
		}
		var page []language.Tuple
		var id string
		for rows.Next() {
			var blob []byte
			var t language.Tuple
			if err = rows.Scan(&id, &blob); err == nil {
				err = json.Unmarshal(blob, &t)
			}
			if err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, t)
			last = id
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, t := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fn(t); err != nil {
				return err
			}
		}
		if len(page) < pageRows {
			return nil
		}
	}
}

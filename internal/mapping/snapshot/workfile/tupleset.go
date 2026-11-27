package workfile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// desiredBatch is how many desired tuples go in one transaction.
const desiredBatch = 1000

// sortedPage is how many tuples Sorted reads before it calls fn, so fn may use
// the work file while it walks.
const sortedPage = 1000

// tupleSet keeps the replay's tuples in the tupleset table.
type tupleSet struct{ f *File }

// TupleSet returns the replay's tuple set kept in the work file's tupleset table.
func (f *File) TupleSet() snapshot.TupleSet { return tupleSet{f} }

func (s tupleSet) Get(ctx context.Context, id string) (snapshot.Stored, bool, error) {
	var blob []byte
	var st snapshot.Stored
	var marked int
	err := s.f.db.QueryRowContext(ctx, `SELECT tuple, writer, typ, marked FROM tupleset WHERE id = ?`, id).
		Scan(&blob, &st.Writer, &st.Type, &marked)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot.Stored{}, false, nil
	}
	if err != nil {
		return snapshot.Stored{}, false, err
	}
	if err := json.Unmarshal(blob, &st.Tuple); err != nil {
		return snapshot.Stored{}, false, err
	}
	st.Marked = marked != 0
	return st, true, nil
}

func (s tupleSet) Put(ctx context.Context, id string, st snapshot.Stored) error {
	blob, err := json.Marshal(st.Tuple)
	if err != nil {
		return err
	}
	marked := 0
	if st.Marked {
		marked = 1
	}
	t := st.Tuple
	_, err = s.f.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO tupleset(id, shape, user, relation, object, tuple, writer, typ, marked) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, snapshot.ShapeKey(t), t.User, t.Relation, t.Object, blob, st.Writer, st.Type, marked)
	return err
}

func (s tupleSet) Delete(ctx context.Context, id string) error {
	_, err := s.f.db.ExecContext(ctx, `DELETE FROM tupleset WHERE id = ?`, id)
	return err
}

// Sorted reads in pages by key, closing each query before fn runs: the work
// file has one connection, and fn may write to it. The default BINARY
// collation compares bytes like Go strings.
func (s tupleSet) Sorted(ctx context.Context, fn func(snapshot.Stored) error) error {
	var last [4]string
	first := true
	for {
		var page []snapshot.Stored
		var keys [][4]string
		q := `SELECT shape, user, relation, object, tuple, writer, typ, marked FROM tupleset `
		args := []any{}
		if !first {
			q += `WHERE (shape, user, relation, object) > (?, ?, ?, ?) `
			args = append(args, last[0], last[1], last[2], last[3])
		}
		q += `ORDER BY shape, user, relation, object LIMIT ?`
		args = append(args, sortedPage)
		rows, err := s.f.db.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k [4]string
			var blob []byte
			var st snapshot.Stored
			var marked int
			if err = rows.Scan(&k[0], &k[1], &k[2], &k[3], &blob, &st.Writer, &st.Type, &marked); err == nil {
				err = json.Unmarshal(blob, &st.Tuple)
			}
			if err != nil {
				_ = rows.Close()
				return err
			}
			st.Marked = marked != 0
			page = append(page, st)
			keys = append(keys, k)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, st := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fn(st); err != nil {
				return err
			}
		}
		if len(page) < sortedPage {
			return nil
		}
		last, first = keys[len(keys)-1], false
	}
}

// AddDesired buffers t for the desired table and writes the buffer when it
// holds a batch.
func (f *File) AddDesired(ctx context.Context, t language.Tuple) error {
	f.desired = append(f.desired, t)
	if len(f.desired) >= desiredBatch {
		return f.FlushDesired(ctx)
	}
	return nil
}

// FlushDesired writes the buffered desired tuples in one transaction.
func (f *File) FlushDesired(ctx context.Context) (err error) {
	if len(f.desired) == 0 {
		return nil
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO desired(id, shape, user, relation, object, tuple) VALUES(?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, t := range f.desired {
		blob, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, snapshot.Identity(t), snapshot.ShapeKey(t), t.User, t.Relation, t.Object, blob); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	f.desired = f.desired[:0]
	return nil
}

// DesiredCount is how many tuples the desired table holds.
func (f *File) DesiredCount() (n int, err error) {
	err = f.db.QueryRow(`SELECT COUNT(*) FROM desired`).Scan(&n)
	return n, err
}

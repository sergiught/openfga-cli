package workfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// diskList is a snapshot.Tuples read from the work file.
type diskList struct {
	count func() (int, error)
	each  func(fn func(language.Tuple) error) error
}

// Len reports 0 when the count fails; Each reports the error.
func (l diskList) Len() int {
	n, err := l.count()
	if err != nil {
		return 0
	}
	return n
}

func (l diskList) Each(fn func(language.Tuple) error) error { return l.each(fn) }

// Desired is the desired table as a list, in the order of its key (shape,
// user, relation, object), 1,000 rows per query.
func (f *File) Desired() snapshot.Tuples {
	return diskList{count: f.DesiredCount, each: f.named(f.eachDesired)}
}

// named makes a read's own errors name the file; an error fn returns passes
// through as it is.
func (f *File) named(each func(fn func(language.Tuple) error) error) func(fn func(language.Tuple) error) error {
	return func(fn func(language.Tuple) error) error {
		var fromFn error
		err := each(func(t language.Tuple) error {
			fromFn = fn(t)
			return fromFn
		})
		if err != nil && (fromFn == nil || err != fromFn) {
			return fmt.Errorf("work file %s: %w", f.path, err)
		}
		return err
	}
}

// Diff returns the diff table's lists, each read in diff key order. Changed
// and Was are the two halves of the same rows, so they stay paired.
func (f *File) Diff() snapshot.DiffLists {
	kind := func(k DiffKind, was bool) snapshot.Tuples {
		return diskList{
			count: func() (n int, err error) {
				err = f.db.QueryRow(`SELECT COUNT(*) FROM diff WHERE kind = ?`, int(k)).Scan(&n)
				return n, err
			},
			each: func(fn func(language.Tuple) error) error {
				return f.DiffRows(context.Background(), k, func(t, w language.Tuple) error {
					if was {
						return fn(w)
					}
					return fn(t)
				})
			},
		}
	}
	return snapshot.DiffLists{Missing: kind(Missing, false), Changed: kind(Changed, false), Was: kind(Changed, true), Stale: kind(Stale, false)}
}

func (f *File) eachDesired(fn func(language.Tuple) error) error {
	var last [4]string
	first := true
	for {
		q := `SELECT shape, user, relation, object, tuple FROM desired `
		var args []any
		if !first {
			q += `WHERE (shape, user, relation, object) > (?, ?, ?, ?) `
			args = append(args, last[0], last[1], last[2], last[3])
		}
		q += `ORDER BY shape, user, relation, object LIMIT ?`
		args = append(args, pageRows)
		rows, err := f.db.Query(q, args...)
		if err != nil {
			return err
		}
		var page []language.Tuple
		var key [4]string
		for rows.Next() {
			var blob []byte
			var t language.Tuple
			if err = rows.Scan(&key[0], &key[1], &key[2], &key[3], &blob); err == nil {
				err = json.Unmarshal(blob, &t)
			}
			if err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, t)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, t := range page {
			if err := fn(t); err != nil {
				return err
			}
		}
		if len(page) < pageRows {
			return nil
		}
		last, first = key, false
	}
}

// Pages calls fn with up to size rows of one kind at a time, in diff key order.
// was is the stored version of each row of a Changed page and nil otherwise. fn
// may call MarkSent; an error from fn stops the walk and is returned as it is.
// It does not skip what MarkSent recorded: that is already out of the diff of
// the next Compare, because MarkSent updates the saved store read.
func (f *File) Pages(kind DiffKind, size int, fn func(page, was []language.Tuple) error) error {
	if size < 1 {
		size = 1
	}
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
		args = append(args, size)
		page, was, key, err := f.readPage(q, args)
		if err != nil {
			return fmt.Errorf("work file %s: %w", f.path, err)
		}
		if len(page) == 0 {
			return nil
		}
		if err := fn(page, was); err != nil {
			return err
		}
		if len(page) < size {
			return nil
		}
		last, first = key, false
	}
}

func (f *File) readPage(q string, args []any) (page, was []language.Tuple, last [4]string, err error) {
	rows, err := f.db.Query(q, args...)
	if err != nil {
		return nil, nil, last, err
	}
	for rows.Next() {
		var blob, wasBlob []byte
		var t, w language.Tuple
		if err = rows.Scan(&last[0], &last[1], &last[2], &last[3], &blob, &wasBlob); err == nil {
			err = json.Unmarshal(blob, &t)
		}
		if err == nil && wasBlob != nil {
			err = json.Unmarshal(wasBlob, &w)
		}
		if err != nil {
			_ = rows.Close()
			return nil, nil, last, err
		}
		page = append(page, t)
		if wasBlob != nil {
			was = append(was, w)
		}
	}
	return page, was, last, errors.Join(rows.Err(), rows.Close())
}

// MarkSent records that the store accepted the changes of page, and applies
// them to the saved store read in the same transaction, so that read goes on
// mirroring the store and a later Compare, with whatever mapping, leaves them
// out. page holds the tuples as Pages gave them: a Missing or Changed row is
// stored with the tuple it wrote, condition and context included, and a Stale
// row is removed.
func (f *File) MarkSent(kind DiffKind, page []language.Tuple) (err error) {
	tx, err := f.db.Begin()
	if err != nil {
		return fmt.Errorf("work file %s: mark sent: %w", f.path, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	mark, err := tx.Prepare(`INSERT INTO sent(kind, id, key) VALUES(?, ?, '')`)
	if err != nil {
		return fmt.Errorf("work file %s: mark sent: %w", f.path, err)
	}
	defer func() { _ = mark.Close() }()
	apply := `INSERT OR REPLACE INTO store(id, tuple) VALUES(?, ?)`
	if kind == Stale {
		apply = `DELETE FROM store WHERE id = ?`
	}
	mirror, err := tx.Prepare(apply)
	if err != nil {
		return fmt.Errorf("work file %s: mark sent: %w", f.path, err)
	}
	defer func() { _ = mirror.Close() }()
	for _, t := range page {
		id := snapshot.Identity(t)
		if _, err = mark.Exec(int(kind), id); err != nil {
			return fmt.Errorf("work file %s: mark sent: %w", f.path, err)
		}
		if kind == Stale {
			_, err = mirror.Exec(id)
		} else {
			var blob []byte
			if blob, err = json.Marshal(t); err == nil {
				_, err = mirror.Exec(id, blob)
			}
		}
		if err != nil {
			return fmt.Errorf("work file %s: mark sent: %w", f.path, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("work file %s: mark sent: %w", f.path, err)
	}
	return nil
}

// SentCount is how many rows of one kind MarkSent recorded.
func (f *File) SentCount(kind DiffKind) (n int, err error) {
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM sent WHERE kind = ?`, int(kind)).Scan(&n); err != nil {
		return 0, fmt.Errorf("work file %s: count sent: %w", f.path, err)
	}
	return n, nil
}

// ResetSent forgets which rows MarkSent recorded. It keeps the counts: Carried
// adds what was dropped here to what was carried already. A run that reads the
// store again calls it, since that read already holds what the store accepted.
// The counts and the deletion are one transaction.
func (f *File) ResetSent() (err error) {
	tx, err := f.db.Begin()
	if err != nil {
		return fmt.Errorf("work file %s: reset sent: %w", f.path, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, kind := range []DiffKind{Missing, Changed, Stale} {
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM sent WHERE kind = ?`, int(kind)).Scan(&n); err != nil {
			return fmt.Errorf("work file %s: reset sent: %w", f.path, err)
		}
		if n == 0 {
			continue
		}
		// Read through tx: the file has one connection, and tx holds it.
		var carried string
		if err = tx.QueryRow(`SELECT COALESCE((SELECT value FROM kv WHERE key = ?), '')`, carriedKey(kind)).Scan(&carried); err != nil {
			return fmt.Errorf("work file %s: reset sent: %w", f.path, err)
		}
		before, _ := strconv.Atoi(carried)
		if _, err = tx.Exec(`INSERT INTO kv(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, carriedKey(kind), strconv.Itoa(before+n)); err != nil {
			return fmt.Errorf("work file %s: reset sent: %w", f.path, err)
		}
	}
	if _, err = tx.Exec(`DELETE FROM sent`); err != nil {
		return fmt.Errorf("work file %s: reset sent: %w", f.path, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("work file %s: reset sent: %w", f.path, err)
	}
	return nil
}

func carriedKey(kind DiffKind) string { return "sent.carried." + strconv.Itoa(int(kind)) }

// Carried is how many rows of one kind earlier runs sent that ResetSent
// forgot.
func (f *File) Carried(kind DiffKind) int {
	n, _ := strconv.Atoi(f.Recall(carriedKey(kind)))
	return n
}

package workfile

import (
	"context"
	"encoding/json"
	"errors"

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
	return diskList{count: f.DesiredCount, each: f.eachDesired}
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

package workfile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

// planState keeps the planner's changes in the plan table.
type planState struct{ f *File }

// PlanState returns the planner's state kept in the work file's plan table.
func (f *File) PlanState() mapping.PlanState { return planState{f} }

func (s planState) Last(ctx context.Context, uro string) (mapping.PlannedChange, bool, error) {
	var blob []byte
	var event int
	err := s.f.db.QueryRowContext(ctx, `SELECT tuple, event FROM plan WHERE uro = ?`, uro).Scan(&blob, &event)
	if errors.Is(err, sql.ErrNoRows) {
		return mapping.PlannedChange{}, false, nil
	}
	if err != nil {
		return mapping.PlannedChange{}, false, err
	}
	pc, err := decodePlanned(blob, event)
	return pc, err == nil, err
}

func (s planState) Plan(ctx context.Context, changes []mapping.PlannedChange) (err error) {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.f.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO plan(uro, otype, user, relation, object, tuple, event, del) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, c := range changes {
		t := c.Tuple
		blob, err := json.Marshal(t)
		if err != nil {
			return err
		}
		typ, _, _ := strings.Cut(t.Object, ":")
		del := 0
		if t.Action == language.ActionDelete {
			del = 1
		}
		if _, err = stmt.ExecContext(ctx, mapping.URO(t), typ, t.User, t.Relation, t.Object, blob, c.Event, del); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Matching mirrors the planner's matchesFilter: user and relation match
// exactly; an object of the form "type:" matches every object with that
// prefix (equality and a range, so LIKE wildcards in a type need no escaping); any other
// object matches exactly.
func (s planState) Matching(ctx context.Context, f language.TupleFilter) ([]mapping.PlannedChange, error) {
	var conds []string
	var args []any
	if f.User != "" {
		conds = append(conds, "user = ?")
		args = append(args, f.User)
	}
	if f.Relation != "" {
		conds = append(conds, "relation = ?")
		args = append(args, f.Relation)
	}
	if f.Object != "" {
		if i := strings.IndexByte(f.Object, ':'); i > 0 && i == len(f.Object)-1 {
			// otype is the part before the first colon, so with the lower bound
			// it selects exactly the objects starting with "type:".
			conds = append(conds, "otype = ?", "object >= ?")
			args = append(args, f.Object[:i], f.Object)
		} else {
			conds = append(conds, "object = ?")
			args = append(args, f.Object)
		}
	}
	q := `SELECT tuple, event FROM plan`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " AND ") //nolint:gosec // conds are the fixed fragments above; values are bound parameters
	}
	rows, err := s.f.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []mapping.PlannedChange
	for rows.Next() {
		var blob []byte
		var event int
		if err := rows.Scan(&blob, &event); err != nil {
			return nil, err
		}
		pc, err := decodePlanned(blob, event)
		if err != nil {
			return nil, err
		}
		out = append(out, pc)
	}
	return out, rows.Err()
}

func decodePlanned(blob []byte, event int) (mapping.PlannedChange, error) {
	var t language.Tuple
	if err := json.Unmarshal(blob, &t); err != nil {
		return mapping.PlannedChange{}, err
	}
	return mapping.PlannedChange{Tuple: t, Event: event}, nil
}

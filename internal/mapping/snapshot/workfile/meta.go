package workfile

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// SchemaVersion is the version of the work file layout.
const SchemaVersion = 1

// MaxAge is how old a work file may be and still be resumed.
const MaxAge = 24 * time.Hour

// Meta describes the run that made a work file.
type Meta struct {
	Command, Provider, Tenant, StoreID string
	Since                              time.Time // zero without --since
	SinceText                          string    // the flag as given
	Types                              []string  // requested snapshot types, in the provider's order
	MappingSHA256                      string
	CreatedAt                          time.Time
}

// RefusalError says why a work file cannot be resumed.
type RefusalError struct{ Path, Reason string }

func (e *RefusalError) Error() string {
	return "cannot resume from " + e.Path + ": " + e.Reason
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

func writeMeta(tx *sql.Tx, m Meta) error {
	types, err := json.Marshal(m.Types)
	if err != nil {
		return err
	}
	rows := [][2]string{
		{"schema_version", strconv.Itoa(SchemaVersion)},
		{"command", m.Command},
		{"provider", m.Provider},
		{"tenant", m.Tenant},
		{"store_id", m.StoreID},
		{"since", formatTime(m.Since)},
		{"since_text", m.SinceText},
		{"types", string(types)},
		{"mapping_sha256", m.MappingSHA256},
		{"created_at", formatTime(m.CreatedAt)},
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, r[0], r[1]); err != nil {
			return err
		}
	}
	return nil
}

func readMetaMap(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func metaFromMap(kv map[string]string) (Meta, error) {
	m := Meta{
		Command:       kv["command"],
		Provider:      kv["provider"],
		Tenant:        kv["tenant"],
		StoreID:       kv["store_id"],
		SinceText:     kv["since_text"],
		MappingSHA256: kv["mapping_sha256"],
	}
	var err error
	if m.Since, err = parseTime(kv["since"]); err != nil {
		return Meta{}, fmt.Errorf("read since: %w", err)
	}
	if m.CreatedAt, err = parseTime(kv["created_at"]); err != nil {
		return Meta{}, fmt.Errorf("read created_at: %w", err)
	}
	if kv["types"] != "" {
		if err := json.Unmarshal([]byte(kv["types"]), &m.Types); err != nil {
			return Meta{}, fmt.Errorf("read types: %w", err)
		}
	}
	return m, nil
}

// Meta returns the metadata saved when the file was made.
func (f *File) Meta() Meta { return f.meta }

// Check reports a *RefusalError when the file cannot be resumed by a run
// described by want. A different mapping is allowed.
func (f *File) Check(want Meta, now time.Time) error {
	refuse := func(format string, a ...any) error {
		return &RefusalError{Path: f.path, Reason: fmt.Sprintf(format, a...)}
	}
	var version string
	if err := f.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return refuse("it has no schema version")
		}
		return fmt.Errorf("read work file %s: %w", f.path, err)
	}
	if version != strconv.Itoa(SchemaVersion) {
		return refuse("it has schema version %s, not %d", version, SchemaVersion)
	}
	have := f.meta
	switch {
	case have.Command != want.Command:
		return refuse("it was made for command %s, not %s", have.Command, want.Command)
	case have.Provider != want.Provider:
		return refuse("it was made for provider %s, not %s", have.Provider, want.Provider)
	case have.Tenant != want.Tenant:
		return refuse("it was made for tenant %s, not %s", have.Tenant, want.Tenant)
	case have.StoreID != want.StoreID:
		return refuse("it was made for store %s, not %s", have.StoreID, want.StoreID)
	case have.SinceText != want.SinceText:
		return refuse("it was made with --since %q, not %q", have.SinceText, want.SinceText)
	}
	for _, t := range want.Types {
		if !slices.Contains(have.Types, t) {
			return refuse("it did not read %s", t)
		}
	}
	for _, t := range have.Types {
		if !slices.Contains(want.Types, t) {
			return refuse("it read %s, which this run does not request", t)
		}
	}
	if now.Sub(have.CreatedAt) > MaxAge {
		return refuse("it is older than %d hours", int(MaxAge/time.Hour))
	}
	return nil
}

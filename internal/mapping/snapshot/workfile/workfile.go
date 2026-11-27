// Package workfile is the SQLite file that holds a mappings run's snapshot
// events, planner state and diff so memory stays flat and a run can resume.
package workfile

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrLocked means another run holds the work file.
var ErrLocked = errors.New("work file is in use by another run")

// File is an open work file, locked to this process.
type File struct {
	db   *sql.DB
	path string
	meta Meta
}

const schema = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE units(unit TEXT PRIMARY KEY, ord INTEGER, started_at TEXT, finished_at TEXT);
CREATE TABLE events(unit TEXT, rank INTEGER, ord INTEGER, item INTEGER, payload BLOB, PRIMARY KEY(rank, ord, unit, item)) WITHOUT ROWID;
CREATE TABLE kv(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE plan(uro TEXT PRIMARY KEY, otype TEXT, user TEXT, relation TEXT, object TEXT, tuple BLOB, event INTEGER, del INTEGER) WITHOUT ROWID;
CREATE INDEX plan_otype ON plan(otype);
CREATE INDEX plan_user ON plan(user);
CREATE INDEX plan_object ON plan(object);
CREATE TABLE tupleset(id TEXT PRIMARY KEY, shape TEXT, user TEXT, relation TEXT, object TEXT, tuple BLOB, writer INTEGER, typ INTEGER, marked INTEGER) WITHOUT ROWID;
CREATE INDEX tupleset_key ON tupleset(shape, user, relation, object);
CREATE TABLE desired(id TEXT PRIMARY KEY, shape TEXT, user TEXT, relation TEXT, object TEXT, tuple BLOB) WITHOUT ROWID;
CREATE INDEX desired_key ON desired(shape, user, relation, object);
CREATE TABLE store(id TEXT PRIMARY KEY, tuple BLOB) WITHOUT ROWID;
CREATE TABLE diff(kind INTEGER, shape TEXT, user TEXT, relation TEXT, object TEXT, id TEXT, tuple BLOB, was BLOB, PRIMARY KEY(kind, shape, user, relation, object)) WITHOUT ROWID;
CREATE TABLE sent(kind INTEGER, id TEXT, key TEXT, PRIMARY KEY(kind, id, key)) WITHOUT ROWID;
`

var sidecars = []string{"-wal", "-shm", "-journal"}

// DefaultPath returns <cacheDir>/ofga/mappings/<stem>-<12 hex>.db, stable for
// one mapping file, store and command.
func DefaultPath(cacheDir, mappingPath, storeID, command string) (string, error) {
	if cacheDir == "" {
		return "", errors.New("no cache directory")
	}
	abs, err := filepath.Abs(mappingPath)
	if err != nil {
		return "", fmt.Errorf("resolve mapping path: %w", err)
	}
	stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	stem = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, stem)
	sum := sha256.Sum256([]byte(abs + "\x00" + storeID + "\x00" + command))
	return filepath.Join(cacheDir, "ofga", "mappings", stem+"-"+hex.EncodeToString(sum[:])[:12]+".db"), nil
}

func isBusy(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()&0xff == sqlite3.SQLITE_BUSY
	}
	return false
}

func dsn(path, mode string, pragmas ...string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	if mode != "" {
		u.RawQuery += "&mode=" + mode
	}
	return u.String()
}

// hasSQLiteHeader reports whether the file starts with the SQLite magic.
func hasSQLiteHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 16)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.Equal(buf[:n], []byte("SQLite format 3\x00")), nil
}

// checkIsWorkFile refuses a non-empty file that is not a work file without
// writing to it. It returns ErrLocked when another run holds the file.
func checkIsWorkFile(path string) error {
	notWork := func(why string) error {
		return fmt.Errorf("%s exists and is not a work file (%s); remove it or choose another path", path, why)
	}
	ok, err := hasSQLiteHeader(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !ok {
		return notWork("not a SQLite database")
	}
	db, err := sql.Open("sqlite", dsn(path, "ro", "busy_timeout(0)"))
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	var v string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v)
	switch {
	case err == nil:
		return nil
	case isBusy(err):
		return fmt.Errorf("%s: %w", path, ErrLocked)
	default:
		return notWork("no work file metadata")
	}
}

func removeFiles(path string) error {
	for _, s := range append([]string{""}, sidecars...) {
		if err := os.Remove(path + s); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, "",
		"journal_mode(WAL)", "synchronous(NORMAL)", "locking_mode(EXCLUSIVE)",
		"busy_timeout(0)", "foreign_keys(ON)"))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	// Take the exclusive lock now rather than on first use.
	if _, err := db.Exec(`BEGIN IMMEDIATE`); err == nil {
		_, err = db.Exec(`COMMIT`)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		return db, nil
	} else {
		_ = db.Close()
		if isBusy(err) {
			return nil, fmt.Errorf("%s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
}

// Create makes a fresh work file at path, replacing an earlier work file.
func Create(path string, m Meta) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create directory for %s: %w", path, err)
	}
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		if err := checkIsWorkFile(path); err != nil {
			return nil, err
		}
		if err := removeFiles(path); err != nil {
			return nil, fmt.Errorf("replace %s: %w", path, err)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	_ = fh.Close()
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*File, error) {
		_ = db.Close()
		_ = removeFiles(path)
		return nil, fmt.Errorf("initialize %s: %w", path, err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(schema); err != nil {
		_ = tx.Rollback()
		return fail(err)
	}
	if err := writeMeta(tx, m); err != nil {
		_ = tx.Rollback()
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	kv, err := readMetaMap(db)
	if err != nil {
		return fail(err)
	}
	saved, err := metaFromMap(kv)
	if err != nil {
		return fail(err)
	}
	return &File{db: db, path: path, meta: saved}, nil
}

// Open opens an existing work file. A missing file reports fs.ErrNotExist.
func Open(path string) (*File, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open work file: %w", err)
	}
	if st.Size() == 0 {
		return nil, fmt.Errorf("%s exists and is not a work file (empty)", path)
	}
	if err := checkIsWorkFile(path); err != nil {
		return nil, err
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	kv, err := readMetaMap(db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read work file %s: %w", path, err)
	}
	m, err := metaFromMap(kv)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read work file %s: %w", path, err)
	}
	return &File{db: db, path: path, meta: m}, nil
}

// Path returns where the file lives.
func (f *File) Path() string { return f.path }

// Close releases the lock.
func (f *File) Close() error { return f.db.Close() }

// Remove closes the file and deletes it with its sidecars.
func (f *File) Remove() error {
	cerr := f.db.Close()
	if err := removeFiles(f.path); err != nil {
		return err
	}
	return cerr
}

package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
	"github.com/sergiught/openfga-cli/internal/filelock"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// Options configures Start.
type Options struct {
	// SavedPath is the persisted playground database. It is only ever read
	// (copied) by Start and replaced atomically by Save.
	SavedPath string
	// Reset ignores an existing SavedPath and starts from the sample stores.
	Reset bool
}

// Session is a playground running on a scratch copy of the database. Nothing
// reaches SavedPath unless Save is called.
type Session struct {
	savedPath  string
	scratch    string
	scratchDB  string
	ds         *dirtyDatastore
	eng        modeltest.Engine
	stopHTTP   func()
	endpoint   string
	workspaces map[string]*modeltest.Workspace
	// savedDigest is SavedPath's content hash at Start (nil if absent). Save
	// refuses to replace a file another session saved in the meantime.
	savedDigest []byte

	mu     sync.Mutex
	closed bool
	// saveFailed makes Close keep the scratch database after a failed Save.
	saveFailed bool
}

// Start launches the embedded server on a scratch database: a copy of
// o.SavedPath if it exists (and !o.Reset), else a fresh database seeded with
// the sample stores. On any failure everything started is stopped and the
// scratch directory removed.
func Start(ctx context.Context, o Options) (_ *Session, err error) {
	scratch, err := os.MkdirTemp("", "ofga-playground-*")
	if err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	s := &Session{
		savedPath: o.SavedPath,
		scratch:   scratch,
		scratchDB: filepath.Join(scratch, "playground.db"),
	}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()

	roots, err := ExtractSamples(filepath.Join(scratch, "samples"))
	if err != nil {
		return nil, fmt.Errorf("extract sample stores: %w", err)
	}
	s.workspaces = make(map[string]*modeltest.Workspace, len(roots))
	for name, root := range roots {
		ws, err := modeltest.LoadWorkspace(root)
		if err != nil {
			return nil, fmt.Errorf("load sample %s: %w", name, err)
		}
		s.workspaces[name] = ws
	}

	restore := false
	if o.SavedPath != "" {
		dst := ""
		if !o.Reset {
			dst = s.scratchDB
		}
		if s.savedDigest, restore, err = loadSaved(o.SavedPath, dst); err != nil {
			return nil, fmt.Errorf("read saved playground %s: %w", o.SavedPath, err)
		}
	}

	inner, err := OpenDatastore(s.scratchDB)
	if err != nil {
		if restore {
			return nil, fmt.Errorf("open saved playground %s: %w (run `ofga playground --reset` to start over)", o.SavedPath, err)
		}
		return nil, fmt.Errorf("open playground database: %w", err)
	}
	s.ds = &dirtyDatastore{OpenFGADatastore: inner}
	// The engine owns the datastore from here on; Close closes both.
	s.eng, err = modeltest.NewEmbeddedEngineOn(s.ds, nil)
	if err != nil {
		inner.Close()
		return nil, fmt.Errorf("start embedded server: %w", err)
	}

	if !restore {
		if err := s.seed(ctx); err != nil {
			return nil, err
		}
		// Seeding is not a user change.
		s.ds.dirty.Store(false)
	}

	s.endpoint, s.stopHTTP, err = modeltest.ServeHTTP(ctx, s.eng)
	if err != nil {
		return nil, fmt.Errorf("serve playground: %w", err)
	}
	return s, nil
}

func (s *Session) seed(ctx context.Context) error {
	for _, name := range SampleNames() {
		ws := s.workspaces[name]
		lm, err := modeltest.LoadModel(filepath.Join(ws.Root, ws.Manifest.Model))
		if err != nil {
			return fmt.Errorf("sample %s: %w", name, err)
		}
		fixtureNames := make([]string, 0, len(ws.Fixtures))
		for n := range ws.Fixtures {
			fixtureNames = append(fixtureNames, n)
		}
		sort.Strings(fixtureNames)
		var tuples []*openfgav1.TupleKey
		for _, n := range fixtureNames {
			t, err := modeltest.LoadFixtureTuples(ws.Fixtures[n])
			if err != nil {
				return fmt.Errorf("sample %s: %w", name, err)
			}
			tuples = append(tuples, t...)
		}
		if _, _, err := modeltest.SetupStore(ctx, s.eng, name, lm.Proto, tuples); err != nil {
			return fmt.Errorf("seed sample %s: %w", name, err)
		}
	}
	return nil
}

// Endpoint is the loopback URL of the playground's OpenFGA HTTP API.
func (s *Session) Endpoint() string { return s.endpoint }

// Workspaces maps each sample store name to its extracted workspace.
func (s *Session) Workspaces() map[string]*modeltest.Workspace { return s.workspaces }

// Dirty reports whether anything was successfully written since Start.
func (s *Session) Dirty() bool { return s.ds != nil && s.ds.dirty.Load() }

// Stop stops the HTTP server, waiting for in-flight requests, and closes the
// engine, so Dirty reflects every write that will ever land. Save and Close
// still work afterwards; Stop is idempotent.
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown()
}

// Save stops the server, then atomically replaces SavedPath with the scratch
// database and removes the scratch dir. On failure SavedPath is untouched.
func (s *Session) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("playground session already closed")
	}
	if err := s.save(); err != nil {
		// The user chose to keep these changes: Close must not delete them.
		s.saveFailed = true
		return fmt.Errorf("%w\nyour changes are kept in %s: copy it to %s, or set OPENFGA_PLAYGROUND_DB to it next time",
			err, s.scratchDB, s.savedPath)
	}
	s.closed = true
	_ = os.RemoveAll(s.scratch)
	return nil
}

func (s *Session) save() error {
	if err := s.shutdown(); err != nil {
		return err
	}
	if err := checkpointWAL(s.scratchDB); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.savedPath), 0o700); err != nil {
		return fmt.Errorf("create playground data dir: %w", err)
	}
	// Another playground may have saved since this one started; overwriting
	// would silently drop its changes. The lock makes check-and-replace atomic
	// across processes, so two saves at once cannot both pass the check.
	unlock, err := filelock.Lock(s.savedPath + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := fileDigest(s.savedPath)
	if err != nil {
		return fmt.Errorf("read saved playground: %w", err)
	}
	if !bytes.Equal(cur, s.savedDigest) {
		return fmt.Errorf("%s was saved by another playground session since this one started; not overwriting it", s.savedPath)
	}
	return copyAtomic(s.scratchDB, s.savedPath)
}

// loadSaved hashes the saved playground at path and, when dst is non-empty,
// copies it there, all from one open so the digest Save later compares
// against is exactly the content this session loaded (a concurrent save
// renaming a new file into place cannot slip between hash and copy).
// It reports a nil digest and restored=false when path does not exist.
func loadSaved(path, dst string) (digest []byte, restored bool, err error) {
	in, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = in.Close() }()
	h := sha256.New()
	if dst == "" {
		if _, err := io.Copy(h, in); err != nil {
			return nil, false, err
		}
		return h.Sum(nil), false, nil
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, err
	}
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		return nil, false, err
	}
	if err := out.Close(); err != nil {
		return nil, false, err
	}
	return h.Sum(nil), true, nil
}

// fileDigest is path's SHA-256, or nil if it does not exist.
func fileDigest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// Close stops everything and removes the scratch dir without saving. It is
// idempotent and safe after Save.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.shutdown()
	if s.saveFailed {
		// Keep the scratch database: it holds the changes Save could not write.
		return err
	}
	if rmErr := os.RemoveAll(s.scratch); err == nil {
		err = rmErr
	}
	return err
}

// shutdown stops HTTP and closes the engine (and with it the database).
func (s *Session) shutdown() error {
	if s.stopHTTP != nil {
		s.stopHTTP()
		s.stopHTTP = nil
	}
	if s.eng != nil {
		eng := s.eng
		s.eng = nil
		if err := eng.Close(); err != nil {
			return fmt.Errorf("close playground server: %w", err)
		}
	}
	return nil
}

// checkpointWAL guarantees no committed page lives only in a -wal file. Closing
// the last SQLite connection normally folds and deletes it; if one is left
// behind, fold it explicitly, and fail rather than save an incomplete file.
func checkpointWAL(dbPath string) error {
	wal := dbPath + "-wal"
	if fi, err := os.Stat(wal); err != nil || fi.Size() == 0 {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("checkpoint playground database: %w", err)
	}
	_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("checkpoint playground database: %w", err)
	}
	if fi, err := os.Stat(wal); err == nil && fi.Size() > 0 {
		return errors.New("checkpoint playground database: write-ahead log still has content")
	}
	return nil
}

// copyAtomic stages src next to dst and renames it into place (0o600), so dst
// is either the old file or the complete new one.
func copyAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("read playground database: %w", err)
	}
	defer func() { _ = in.Close() }()
	f, err := atomicfile.Create(dst, 0o600)
	if err != nil {
		return err
	}
	defer f.Abort()
	if _, err := io.Copy(f, in); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return f.Commit()
}

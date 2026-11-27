package workfile

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testMeta() Meta {
	return Meta{
		Command: "backfill", Provider: "auth0", Tenant: "t.auth0.com", StoreID: "01HA",
		SinceText: "24h", Since: time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC),
		Types:         []string{"snapshot.user", "snapshot.organization"},
		MappingSHA256: "abc", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 7, time.UTC),
	}
}

func TestDefaultPathIsStablePerMappingStoreCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("my map.yaml", nil, 0o600))
	abs := filepath.Join(dir, "my map.yaml")

	a, err := DefaultPath("/cache", abs, "S1", "backfill")
	require.NoError(t, err)
	again, _ := DefaultPath("/cache", abs, "S1", "backfill")
	rel, _ := DefaultPath("/cache", "my map.yaml", "S1", "backfill")
	require.Equal(t, a, again)
	require.Equal(t, a, rel)
	store, _ := DefaultPath("/cache", abs, "S2", "backfill")
	cmd, _ := DefaultPath("/cache", abs, "S1", "drift")
	require.NotEqual(t, a, store)
	require.NotEqual(t, a, cmd)
	if !regexp.MustCompile(`^/cache/ofga/mappings/my_map-[0-9a-f]{12}\.db$`).MatchString(a) {
		t.Fatalf("unexpected path %s", a)
	}
}

func TestCreateModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "w.db")
	f, err := Create(path, testMeta())
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	st, err = os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), st.Mode().Perm())
}

func TestCreateReplacesAWorkFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	f, err := Create(path, testMeta())
	require.NoError(t, err)
	require.NoError(t, f.Close())
	m := testMeta()
	m.StoreID = "01HB"
	f, err = Create(path, m)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	require.Equal(t, m, f.Meta())
	require.Equal(t, path, f.Path())
}

func TestCreateRoundTripsMetaThroughOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	f, err := Create(path, testMeta())
	require.NoError(t, err)
	require.NoError(t, f.Close())
	g, err := Open(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	require.Equal(t, testMeta(), g.Meta())
	require.NoError(t, g.Check(testMeta(), testMeta().CreatedAt.Add(time.Hour)))
}

func TestCreateRefusesAForeignFile(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "text.db")
	require.NoError(t, os.WriteFile(text, []byte("not a work file"), 0o600))

	other := filepath.Join(dir, "other.db")
	db, err := sql.Open("sqlite", other)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE x(a)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for _, s := range sidecars {
		_ = os.Remove(other + s)
	}
	otherBytes, err := os.ReadFile(other)
	require.NoError(t, err)

	for path, want := range map[string][]byte{text: []byte("not a work file"), other: otherBytes} {
		_, err := Create(path, testMeta())
		require.Error(t, err)
		require.Contains(t, err.Error(), path)
		got, rerr := os.ReadFile(path)
		require.NoError(t, rerr)
		require.Equal(t, want, got)
		for _, s := range sidecars {
			require.NoFileExists(t, path+s)
		}
	}

	empty := filepath.Join(dir, "empty.db")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	f, err := Create(empty, testMeta())
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestOpenMissing(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "none.db"))
	require.Error(t, err)
	require.True(t, errors.Is(err, fs.ErrNotExist))
}

func TestOpenRefusesAForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	require.NoError(t, os.WriteFile(path, []byte("not a work file"), 0o600))
	_, err := Open(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), path)
}

func TestSecondOpenIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	f, err := Create(path, testMeta())
	require.NoError(t, err)

	_, err = Open(path)
	require.ErrorIs(t, err, ErrLocked)
	_, err = Create(path, testMeta())
	require.ErrorIs(t, err, ErrLocked)

	require.NoError(t, f.Close())
	g, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, g.Close())
}

func TestRemoveDeletesFileAndSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	f, err := Create(path, testMeta())
	require.NoError(t, err)
	require.NoError(t, f.Remove())
	for _, s := range append([]string{""}, sidecars...) {
		require.NoFileExists(t, path+s)
	}
}

func TestCheckRefusals(t *testing.T) {
	base := testMeta()
	now := base.CreatedAt.Add(time.Hour)
	tests := []struct {
		name   string
		want   func(Meta) Meta
		now    time.Time
		schema bool
		reason string
	}{
		{"command", func(m Meta) Meta { m.Command = "drift"; return m }, now, false, "command backfill, not drift"},
		{"provider", func(m Meta) Meta { m.Provider = "other"; return m }, now, false, "provider auth0, not other"},
		{"tenant", func(m Meta) Meta { m.Tenant = "x.auth0.com"; return m }, now, false, "tenant t.auth0.com, not x.auth0.com"},
		{"store", func(m Meta) Meta { m.StoreID = "01HB"; return m }, now, false, "it was made for store 01HA, not 01HB"},
		{"since", func(m Meta) Meta { m.SinceText = "1h"; return m }, now, false, "--since"},
		{"schema", func(m Meta) Meta { return m }, now, true, "it has schema version 2, not 1"},
		{"age", func(m Meta) Meta { return m }, base.CreatedAt.Add(MaxAge + time.Second), false, "it is older than 24 hours"},
		{"added type", func(m Meta) Meta {
			m.Types = append(append([]string{}, m.Types...), "snapshot.organization.connection")
			return m
		}, now, false, "it did not read snapshot.organization.connection"},
		{"removed type", func(m Meta) Meta { m.Types = m.Types[:1]; return m }, now, false, "it read snapshot.organization, which this run does not request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Create(filepath.Join(t.TempDir(), "w.db"), base)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()
			if tt.schema {
				_, err := f.db.Exec(`UPDATE meta SET value = '2' WHERE key = 'schema_version'`)
				require.NoError(t, err)
			}
			err = f.Check(tt.want(base), tt.now)
			var re *RefusalError
			if !errors.As(err, &re) {
				t.Fatalf("want *RefusalError, got %v", err)
			}
			require.Equal(t, f.Path(), re.Path)
			if !strings.Contains(re.Reason, tt.reason) {
				t.Fatalf("reason %q lacks %q", re.Reason, tt.reason)
			}
			require.Equal(t, "cannot resume from "+f.Path()+": "+re.Reason, err.Error())
		})
	}

	t.Run("different mapping is fine", func(t *testing.T) {
		f, err := Create(filepath.Join(t.TempDir(), "w.db"), base)
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		w := base
		w.MappingSHA256 = "different"
		require.NoError(t, f.Check(w, now))
	})
}

type requireT struct{}

var require requireT

func (requireT) NoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func (requireT) Error(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
}

func (requireT) ErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error %v is not %v", err, target)
	}
}

func (requireT) True(t *testing.T, v bool) {
	t.Helper()
	if !v {
		t.Fatal("expected true")
	}
}

func (requireT) Equal(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func (requireT) NotEqual(t *testing.T, a, b any) {
	t.Helper()
	if reflect.DeepEqual(a, b) {
		t.Fatalf("both are %#v", a)
	}
}

func (requireT) Contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q lacks %q", s, sub)
	}
}

func (requireT) NoFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("%s exists", path)
	}
}

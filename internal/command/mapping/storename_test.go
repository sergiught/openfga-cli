package mapping

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// storeNameRun runs one command that looks up the store's name before it
// writes, against fs, with --force so it writes without asking.
type storeNameRun struct {
	name string
	run  func(t *testing.T, fs *fakeStore) (stderr string, err error)
}

func storeNameRuns() []storeNameRun {
	return []storeNameRun{
		{"apply", func(t *testing.T, fs *fakeStore) (string, error) {
			mp, ep := applyFiles(t, membership, twoEvents)
			_, stderr, err := runApply(t, context.Background(), fs.cli(), nil, mp, "--events", ep, "--force")
			return stderr, err
		}},
		{"backfill", func(t *testing.T, fs *fakeStore) (string, error) {
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
			mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
			_, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
			return stderr, err
		}},
		{"backfill -o json", func(t *testing.T, fs *fakeStore) (string, error) {
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
			mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
			c := fs.cli()
			c.JSON = true
			_, stderr, err := runBackfill(t, c, nil, mp, "--provider", "fake", "--force")
			return stderr, err
		}},
		{"drift", func(t *testing.T, fs *fakeStore) (string, error) {
			fs.tuples = append(fs.tuples, member("u1"))
			useFakeProvider(t, fakeProvider{types: fakeTypes(), result: memberResult("u1", "u2")})
			_, stderr, err := runDrift(t, fs.cli(), nil, driftArgs(t, driftMapping, "--force")...)
			return stderr, err
		}},
	}
}

// A credential may be allowed to read and write tuples but not to read the
// store itself. The name is only for display, so the commands go on and show
// the store's id instead.
func TestStoreNameLookupRejectedFallsBackToTheID(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		for _, r := range storeNameRuns() {
			t.Run(fmt.Sprintf("%s/%d", r.name, status), func(t *testing.T) {
				configtest.Isolate(t)
				fs := newFakeStore(t)
				fs.getStoreStatus = status
				stderr, err := r.run(t, fs)
				if err != nil {
					t.Fatalf("err = %v\n%s", err, stderr)
				}
				if fs.writeCount() == 0 {
					t.Fatalf("nothing written\n%s", stderr)
				}
				if !strings.Contains(r.name, "json") && !strings.Contains(stderr, "store "+applyStoreID) {
					t.Errorf("stderr does not name the store by its id:\n%s", stderr)
				}
				if strings.Contains(stderr, "acme") {
					t.Errorf("stderr names the store although its name was not read:\n%s", stderr)
				}
			})
		}
	}
}

// Any other failure of the lookup stops the command before it writes: it is
// not one the name's absence explains.
func TestStoreNameLookupOtherFailuresStayFatal(t *testing.T) {
	for _, r := range storeNameRuns() {
		t.Run(r.name, func(t *testing.T) {
			configtest.Isolate(t)
			fs := newFakeStore(t)
			fs.getStoreStatus = http.StatusBadRequest
			stderr, err := r.run(t, fs)
			if err == nil {
				t.Fatalf("a 400 on the store lookup did not fail the command\n%s", stderr)
			}
			if fs.writeCount() != 0 {
				t.Fatalf("wrote %d requests after the lookup failed", fs.writeCount())
			}
		})
	}
}

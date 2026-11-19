package mapping

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// TestSnapshotEndToEndAuth0 reads a real Auth0 tenant (read-only) through the
// example mapping into an embedded OpenFGA server, with both snapshot
// commands. It needs AUTH0_DOMAIN, AUTH0_CLIENT_ID and AUTH0_CLIENT_SECRET and
// is skipped without them.
func TestSnapshotEndToEndAuth0(t *testing.T) {
	for _, k := range []string{"AUTH0_DOMAIN", "AUTH0_CLIENT_ID", "AUTH0_CLIENT_SECRET"} {
		if os.Getenv(k) == "" {
			t.Skip("set AUTH0_DOMAIN, AUTH0_CLIENT_ID and AUTH0_CLIENT_SECRET to run against a live Auth0 tenant")
		}
	}
	configtest.Isolate(t)
	ctx := context.Background()

	lm, err := modeltest.LoadModel(exampleDir + "/model.fga")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := modeltest.NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	storeID, modelID, err := eng.Setup(ctx, lm.Proto, nil)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, stop, err := modeltest.ServeHTTP(ctx, eng)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: endpoint, StoreID: storeID, ModelID: modelID})
	cl, err := openfga.NewClient(endpoint, openfga.WithStoreID(storeID), openfga.WithAuthorizationModelID(modelID))
	if err != nil {
		t.Fatal(err)
	}
	mp := exampleDir + "/auth0.yaml"

	// runCmd runs one snapshot command and logs it with its output.
	runCmd := func(asJSON bool, name string, args ...string) (stdout string, err error) {
		t.Helper()
		c := cli.New(log.New(io.Discard), cfg, "test")
		c.JSON = asJSON
		args = append([]string{mp, "--provider", "auth0"}, args...)
		stdout, stderr, err := runSnapshotCmd(t, c, nil, name, args...)
		if !asJSON {
			t.Logf("$ ofga mappings %s %s\n--- stdout\n%s--- stderr\n%s--- exit code %d", name, strings.Join(args, " "), stdout, stderr, clierr.Code(err))
		}
		return stdout, err
	}
	drift := func(args ...string) (driftResult, error) {
		t.Helper()
		out, err := runCmd(true, "drift", args...)
		var res driftResult
		if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
			t.Fatalf("drift is not one JSON document: %v (err = %v)\n%s", jerr, err, out)
		}
		return res, err
	}
	// diverge deletes one member tuple and writes a user the tenant does not have.
	var gone openfga.TupleKey
	diverge := func(tuples []tupleOut) {
		t.Helper()
		for _, tp := range tuples {
			if tp.Relation == "member" {
				// The first member tuple: the ghost needs an organization object.
				gone = openfga.NewTupleKey(tp.User, tp.Relation, tp.Object)
				break
			}
		}
		if gone.Object == "" {
			t.Fatal("no member tuple to remove")
		}
		ghost := openfga.NewTupleKey("user:ofga-e2e-ghost", "member", gone.Object)
		for _, req := range []*openfga.WriteRequest{
			{Deletes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{gone}}},
			{Writes: &openfga.WriteRequestTuples{TupleKeys: []openfga.TupleKey{ghost}}},
		} {
			if err := cl.Tuples.Write(ctx, req); err != nil {
				t.Fatal(err)
			}
		}
	}
	wantCode := func(step string, err error, want int) {
		t.Helper()
		if got := clierr.Code(err); got != want {
			t.Fatalf("%s: exit code %d (err = %v), want %d", step, got, err, want)
		}
	}

	// 1. backfill: dry run writes nothing, --force writes every tuple.
	out, err := runCmd(true, "backfill", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var bf backfillResult
	if err := json.Unmarshal([]byte(out), &bf); err != nil {
		t.Fatalf("backfill --dry-run: %v\n%s", err, out)
	}
	if len(bf.Tuples) < 12 || bf.Sent.Writes != 0 {
		t.Fatalf("dry run: %d tuples, %d writes sent", len(bf.Tuples), bf.Sent.Writes)
	}
	if got := storeTuples(t, cl); len(got) != 0 {
		t.Fatalf("dry run wrote %d tuples", len(got))
	}
	out, err = runCmd(true, "backfill", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &bf); err != nil {
		t.Fatalf("backfill --force: %v\n%s", err, out)
	}
	if len(bf.Tuples) < 12 || bf.Sent.Writes != len(bf.Tuples) || bf.StartedAt.IsZero() || bf.FinishedAt.Before(bf.StartedAt) {
		t.Fatalf("backfill: %d tuples, sent %+v, window %v..%v", len(bf.Tuples), bf.Sent, bf.StartedAt, bf.FinishedAt)
	}
	if got := storeTuples(t, cl); len(got) != len(bf.Tuples) {
		t.Fatalf("store holds %d tuples, want %d", len(got), len(bf.Tuples))
	}

	// 2. a fresh backfill leaves nothing to fix.
	res, err := drift("--dry-run")
	wantCode("drift after backfill", err, 0)
	if len(res.Missing)+len(res.Changed)+len(res.Stale) != 0 {
		t.Fatalf("drift after backfill: %+v", res)
	}

	// 3. one tuple missing and one stale.
	diverge(bf.Tuples)
	res, err = drift("--dry-run")
	wantCode("drift --dry-run", err, 3)
	if len(res.Missing) != 1 || len(res.Stale) != 1 || len(res.Changed) != 0 || res.Sent.Writes+res.Sent.Deletes != 0 {
		t.Fatalf("drift --dry-run: %+v", res)
	}
	sameTuple := func(got tupleOut, user, relation, object string) bool {
		return got.User == user && got.Relation == relation && got.Object == object
	}
	if !sameTuple(res.Stale[0], "user:ofga-e2e-ghost", "member", gone.Object) ||
		!sameTuple(res.Missing[0], gone.User, gone.Relation, gone.Object) {
		t.Fatalf("drift --dry-run named the wrong tuples: missing %+v, stale %+v", res.Missing, res.Stale)
	}

	if res.StoreReadAt.IsZero() || res.StartedAt.IsZero() || res.StoreReadAt.After(res.StartedAt) {
		t.Fatalf("store read at %v must not be after the snapshot started at %v", res.StoreReadAt, res.StartedAt)
	}

	// 4. --force fixes the missing tuple, and the stale one keeps the exit code at 3.
	res, err = drift("--force")
	wantCode("drift --force", err, 3)
	if res.Sent.Writes != 1 || res.Sent.Deletes != 0 || res.Pruned != 0 || len(res.Stale) != 1 {
		t.Fatalf("drift --force: %+v", res)
	}

	// 5. --prune --force removes it.
	res, err = drift("--prune", "--force")
	wantCode("drift --prune --force", err, 0)
	if res.Pruned != 1 || res.Sent.Deletes != 1 {
		t.Fatalf("drift --prune --force: %+v", res)
	}

	// 6. the store matches the tenant again.
	res, err = drift("--dry-run")
	wantCode("drift at the end", err, 0)
	if len(res.Missing)+len(res.Changed)+len(res.Stale) != 0 {
		t.Fatalf("drift at the end: %+v", res)
	}

	// The same run, read as a user would: a fresh empty store first.
	storeID, modelID, err = eng.Setup(ctx, lm.Proto, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Set("default", config.Profile{APIURL: endpoint, StoreID: storeID, ModelID: modelID})
	if cl, err = openfga.NewClient(endpoint, openfga.WithStoreID(storeID), openfga.WithAuthorizationModelID(modelID)); err != nil {
		t.Fatal(err)
	}
	human := func(name string, want int, args ...string) {
		t.Helper()
		_, err := runCmd(false, name, args...)
		wantCode("human "+name+" "+strings.Join(args, " "), err, want)
	}
	human("backfill", 0, "--dry-run")
	human("backfill", 0, "--force")
	human("drift", 0, "--dry-run")
	diverge(bf.Tuples)
	human("drift", 3, "--dry-run")
	human("drift", 3, "--force")
	human("drift", 0, "--prune", "--force")
}

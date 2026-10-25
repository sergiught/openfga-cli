package mapping

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openfga/mapper/language"
	"github.com/sergiught/go-openfga/openfga"
)

func TestStoreClientReadsEveryPage(t *testing.T) {
	pages := []string{
		`{"tuples":[{"key":{"user":"user:1","relation":"member","object":"team:1"}}],"continuation_token":"p2"}`,
		`{"tuples":[{"key":{"user":"user:2","relation":"member","object":"team:1","condition":{"name":"c","context":{"n":5}}}}],"continuation_token":""}`,
	}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, pages[n])
		n++
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	got, err := storeClient{cl}.ReadTuples(context.Background(), language.TupleFilter{Relation: "member", Object: "team:1"})
	if err != nil || len(got) != 2 || got[1].Condition != "c" || got[1].Context["n"] != float64(5) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestStoreClientRejectsAnUnreadableFilter(t *testing.T) {
	_, err := storeClient{}.ReadTuples(context.Background(), language.TupleFilter{Relation: "member", Object: "team:"})
	if err == nil || !strings.Contains(err.Error(), "team:") {
		t.Fatalf("err = %v", err)
	}
}

func TestStoreClientWritesOneRequestIgnoringDuplicatesAndMissing(t *testing.T) {
	var req openfga.WriteRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	err := storeClient{cl}.Write(context.Background(),
		[]language.Tuple{{User: "user:1", Relation: "member", Object: "team:1", Condition: "c", Context: map[string]any{"n": 1.0}}},
		[]language.Tuple{{User: "user:2", Relation: "member", Object: "team:1", Condition: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if req.Writes.OnDuplicate != openfga.OnDuplicateIgnore || req.Deletes.OnMissing != openfga.OnMissingIgnore {
		t.Fatalf("req = %+v", req)
	}
	if req.Writes.TupleKeys[0].Condition == nil || req.Deletes.TupleKeys[0].Condition != nil {
		t.Fatalf("conditions: writes keep, deletes drop: %+v", req)
	}
}

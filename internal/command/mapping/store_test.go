package mapping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestReadTuplesAsksForOneHundredAPage(t *testing.T) {
	var got struct {
		PageSize int `json:"page_size"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"tuples":[],"continuation_token":""}`)
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	if _, err := (storeClient{cl}).ReadTuples(context.Background(), language.TupleFilter{}); err != nil {
		t.Fatal(err)
	}
	if got.PageSize != 100 {
		t.Fatalf("page_size = %d, want 100", got.PageSize)
	}
}

func TestReadKeptKeepsOnlyWhatIsAskedForAndCountsEverything(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tuples":[`+
			`{"key":{"user":"user:1","relation":"member","object":"team:1"}},`+
			`{"key":{"user":"user:1","relation":"viewer","object":"doc:1"}}],"continuation_token":""}`)
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	kept, total, err := storeClient{cl}.ReadKept(context.Background(), func(t language.Tuple) bool { return t.Relation == "member" }, nil, nil)
	if err != nil || total != 2 || len(kept) != 1 || kept[0].Object != "team:1" {
		t.Fatalf("kept=%+v total=%d err=%v", kept, total, err)
	}
}

func TestReadKeptReportsProgressAtTheEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tuples":[`+
			`{"key":{"user":"user:1","relation":"member","object":"team:1"}},`+
			`{"key":{"user":"user:1","relation":"viewer","object":"doc:1"}}],"continuation_token":""}`)
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	var reports [][2]int
	_, _, err := storeClient{cl}.ReadKept(context.Background(), func(t language.Tuple) bool { return t.Relation == "member" }, nil,
		func(read, kept int) { reports = append(reports, [2]int{read, kept}) })
	if err != nil || len(reports) != 1 || reports[0] != [2]int{2, 1} {
		t.Fatalf("reports=%v err=%v", reports, err)
	}
}

func TestStoreClientRecognisesTheWriteLimit(t *testing.T) {
	const msg = "The number of write operations exceeds the allowed limit of 40"
	resp := func(status int) *http.Response { return &http.Response{StatusCode: status} }
	tooMany := &openfga.ErrorResponse{Response: resp(http.StatusBadRequest), Code: "exceeded_entity_limit", Message: msg}
	tests := []struct {
		name      string
		err       error
		wantLimit int
		wantOK    bool
	}{
		{"limit named", tooMany, 40, true},
		{"wrapped", fmt.Errorf("write: %w", tooMany), 40, true},
		{"no number", &openfga.ErrorResponse{Response: resp(http.StatusBadRequest), Code: "exceeded_entity_limit", Message: "too many"}, 0, true},
		{"code dropped by a proxy", &openfga.ErrorResponse{Response: resp(http.StatusBadRequest), Message: msg}, 40, true},
		{"condition conflict", &openfga.ErrorResponse{Response: resp(http.StatusConflict), Code: "write_failed_due_to_invalid_input", Message: "already exists with a different condition"}, 0, false},
		{"plain error", errors.New("boom"), 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			limit, ok := storeClient{}.BatchLimit(tc.err)
			if limit != tc.wantLimit || ok != tc.wantOK {
				t.Errorf("BatchLimit() = (%d, %v), want (%d, %v)", limit, ok, tc.wantLimit, tc.wantOK)
			}
		})
	}
}

func TestReadKeptSink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tuples":[`+
			`{"key":{"user":"user:1","relation":"member","object":"team:1"}},`+
			`{"key":{"user":"user:1","relation":"viewer","object":"doc:1"}},`+
			`{"key":{"user":"user:2","relation":"member","object":"team:1"}}],"continuation_token":""}`)
	}))
	defer srv.Close()
	cl, _ := openfga.NewClient(srv.URL, openfga.WithStoreID("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	var got []language.Tuple
	kept, total, err := storeClient{cl}.ReadKept(context.Background(), func(t language.Tuple) bool { return t.Relation == "member" },
		func(ts []language.Tuple) error { got = append(got, ts...); return nil }, nil)
	if err != nil || total != 3 || kept != nil || len(got) != 2 || got[0].User != "user:1" || got[1].User != "user:2" {
		t.Fatalf("kept=%+v got=%+v total=%d err=%v", kept, got, total, err)
	}
}

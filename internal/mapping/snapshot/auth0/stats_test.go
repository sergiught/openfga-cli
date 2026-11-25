package auth0

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

func TestStatsBeforeAnyReadIsZero(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, tenant{n: 1}.handler())

	equal(t, snapshot.Stats{}, p.Stats())
}

func TestStatsCountRequestsRateLimitsAndItems(t *testing.T) {
	t.Parallel()

	var served, limited atomic.Int64

	p, _ := newProvider(t, tenant{n: 6, before: func(w http.ResponseWriter, _ *http.Request) bool {
		if served.Add(1) > 4 {
			return false
		}

		limited.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)

		return true
	}}.handler())

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: tenantTypes}, nil)
	noErr(t, err)

	st := p.Stats()
	equal(t, int(served.Load()), st.Requests)
	equal(t, int(limited.Load()), st.RateLimited)
	equal(t, 4, st.RateLimited)
	equal(t, res.RateLimited, st.RateLimited)
	equal(t, res.Counts, st.Counts)
}

func TestStatsCountsOnlyRequestedTypesInOrder(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, tenant{n: 3}.handler())

	res, err := p.Snapshot(context.Background(),
		snapshot.Request{Types: []string{"snapshot.organization.member", "snapshot.organization"}}, nil)
	noErr(t, err)
	equal(t, 2, len(p.Stats().Counts))
	equal(t, res.Counts, p.Stats().Counts)
}

func TestStatsDuringAParallelReadDoNotRace(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, tenant{n: 20}.handler())

	var wg sync.WaitGroup

	stop := make(chan struct{})

	wg.Add(1)

	go func() {
		defer wg.Done()

		for {
			select {
			case <-stop:
				return
			default:
				_ = p.Stats()
			}
		}
	}()

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: tenantTypes}, nil)

	close(stop)
	wg.Wait()
	noErr(t, err)
	equal(t, res.Counts, p.Stats().Counts)
}

func TestEachReportsStepDoneAndTotal(t *testing.T) {
	t.Parallel()

	const n = 12

	p, _ := newProvider(t, tenant{n: n}.handler())

	var (
		mu  sync.Mutex
		got []snapshot.Progress
	)

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: tenantTypes}, func(pr snapshot.Progress) {
		mu.Lock()
		defer mu.Unlock()

		got = append(got, pr)
	})
	noErr(t, err)

	for _, step := range []string{"connections of organizations", "members of organizations"} {
		last := 0

		for _, pr := range got {
			if pr.Step != step {
				continue
			}

			equal(t, n, pr.Total)

			if pr.Done <= last {
				t.Errorf("%s: Done %d after %d", step, pr.Done, last)
			}

			last = pr.Done
		}

		equal(t, n, last)
	}
}

func TestListPagesReportWithoutAMessage(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, tenant{n: 3}.handler())

	var got []snapshot.Progress

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, func(pr snapshot.Progress) {
		got = append(got, pr)
	})
	noErr(t, err)
	equal(t, []snapshot.Progress{
		{Message: "reading organizations", Step: "organizations"},
		{Step: "organizations", Done: 3},
	}, got)
}

func TestExportReportsJobAndDownloadSteps(t *testing.T) {
	t.Parallel()

	f := &exportFake{
		statuses: []string{
			`{"status":"processing","type":"users_export","id":"job_1","percentage_done":40}`,
			`{"status":"completed","type":"users_export","id":"job_1","location":"LOCATION"}`,
		},
		file: gzipLines(filled(2500)...),
	}
	p, _ := newExportProvider(t, 2500, f)

	var got []snapshot.Progress

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, func(pr snapshot.Progress) {
		got = append(got, pr)
	})
	noErr(t, err)
	equal(t, 2500, res.Counts[0].N)
	equal(t, res.Counts, p.Stats().Counts)

	var download []int

	for _, pr := range got {
		switch pr.Step {
		case "users (export job)":
			equal(t, 2500, pr.Total)
		case "users (export download)":
			equal(t, 2500, pr.Total)

			download = append(download, pr.Done)
		}
	}

	equal(t, []int{1000, 2000, 2500}, download)
	equal(t, snapshot.Progress{Message: "exporting users: 40%", Step: "users (export job)", Done: 1000, Total: 2500}, got[len(got)-4])
}

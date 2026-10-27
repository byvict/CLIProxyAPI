package submon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerIsolationCoalescingAndShutdown(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := testTime()
	blocked := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	sources := []source{
		{ID: "blocked", Provider: codex, Cadence: 5 * time.Minute, Probe: func(c probeContext) probe {
			calls.Add(1)
			close(entered)
			select {
			case <-blocked:
			case <-c.Context.Done():
			}
			return successfulProbe(now, "blocked")
		}},
		{ID: "working", Provider: grok, Cadence: 5 * time.Minute, Probe: func(c probeContext) probe {
			p := successfulProbe(now, "working")
			p.SourceID = "working"
			close(done)
			return p
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	workers := newCollector(s, sources, func() time.Time { return now })
	errs := make(chan error, 1)
	go func() { errs <- workers.run(ctx) }()
	<-entered
	<-done
	for i := 0; i < 50; i++ {
		workers.refresh("blocked")
	}
	if calls.Load() != 1 {
		t.Fatal("overlapping probe")
	}
	info := workers.states()
	if info["blocked"].InFlightSince == nil || !info["blocked"].NextAt.Equal(now.Add(5*time.Minute)) {
		t.Fatal("pending not visible")
	}
	cancel()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("queued requests bypassed cadence")
	}
}
func TestBackoffAndMuseFloor(t *testing.T) {
	now := testTime()
	for _, p := range []provider{codex, muse} {
		next, failures := nextProbe(now, cadence(p), 3, missing{Status: rateLimited, RetryAt: now.Add(2 * time.Hour)})
		if next.Before(now.Add(2*time.Hour)) || failures != 4 {
			t.Fatal("retry after ignored")
		}
	}
	next, _ := nextProbe(now, cadence(muse), 0, reading{})
	if next.Sub(now) < 30*time.Minute {
		t.Fatal("mint floor bypassed")
	}
}

func TestStalledHTTPSourceDoesNotBlockPersistenceOrAPI(t *testing.T) {
	workingProbeDone := make(chan struct{})
	entered := make(chan struct{})
	working := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			close(entered)
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{}`))
		close(working)
	}))
	defer server.Close()
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	client := quotaClient()
	defer client.CloseIdleConnections()
	r := quotaReader{client: client}
	sources := []source{}
	for _, id := range []string{"blocked", "working"} {
		sourceID := id
		sources = append(sources, source{ID: sourceID, Provider: codex, Cadence: 5 * time.Minute, Probe: func(c probeContext) probe {
			_, missing := r.request(c.Context, c.Now(), server.URL+"/"+sourceID, "fixture-secret", nil, false)
			if missing != nil {
				return probe{Outcome: *missing}
			}
			if sourceID == "working" {
				close(workingProbeDone)
			}
			return successfulProbe(testTime(), sourceID)
		}})
	}
	c := newCollector(s, sources, testTime)
	for i := 0; i < 50; i++ {
		c.refresh("blocked")
	}
	if len(c.workers["blocked"].kick) != 1 {
		t.Fatal("refresh did not coalesce")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.run(ctx) }()
	<-entered
	<-working
	<-workingProbeDone
	handler := newAPI(s, c, "fixture-key", testTime, "127.0.0.1:8318")
	w := requestAPI(handler, "GET", "/api/v1/snapshot", "", "fixture-key", "", "127.0.0.1:8318", nil)
	if w.Code != 200 {
		t.Fatal("UI stalled", w.Code)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil || len(data.Accounts) != 1 {
		t.Fatal("other source did not persist")
	}
}
func TestCollectorRespectsPersistedReservationAfterRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	next := testTime().Add(2 * time.Hour)
	if err = s.reserve("muse-native", next, 4); err != nil {
		t.Fatal(err)
	}
	if err = s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	var calls atomic.Int32
	c := newCollector(s, []source{{ID: "muse-native", Provider: muse, Cadence: 30 * time.Minute, Probe: func(probeContext) probe { calls.Add(1); return probe{Outcome: missing{Status: unavailable}} }}}, testTime)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = c.run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || !c.states()["muse-native"].NextAt.Equal(next) {
		t.Fatal("restart bypassed backoff")
	}
}

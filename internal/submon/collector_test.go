package submon

import (
	"context"
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
	if info["blocked"].InFlightSince == nil {
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

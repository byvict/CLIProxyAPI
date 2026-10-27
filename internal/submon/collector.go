package submon

import (
	"context"
	"errors"
	"sync"
	"time"
)

type workerState struct {
	InFlightSince *time.Time `json:"inFlightSince"`
	NextAt        time.Time  `json:"nextAt"`
	NotBefore     time.Time  `json:"notBefore"`
	Queued        bool       `json:"queued"`
	Failures      int        `json:"failures"`
}
type sourceWorker struct {
	source source
	kick   chan struct{}
	mu     sync.Mutex
	state  workerState
}
type collector struct {
	store   *store
	workers map[string]*sourceWorker
	now     func() time.Time
}

func newCollector(s *store, sources []source, now func() time.Time) *collector {
	c := &collector{store: s, workers: map[string]*sourceWorker{}, now: func() time.Time { return now().Round(0) }}
	for _, src := range sources {
		c.workers[src.ID] = &sourceWorker{source: src, kick: make(chan struct{}, 1)}
	}
	return c
}
func (c *collector) states() map[string]workerState {
	out := map[string]workerState{}
	for id, w := range c.workers {
		w.mu.Lock()
		out[id] = w.state
		w.mu.Unlock()
	}
	return out
}
func (c *collector) refresh(id string) map[string]workerState {
	for name, w := range c.workers {
		if id != "" && id != name {
			continue
		}
		w.mu.Lock()
		if w.state.InFlightSince == nil && !c.now().Before(w.state.NotBefore) {
			select {
			case w.kick <- struct{}{}:
				w.state.Queued = true
			default:
			}
		}
		w.mu.Unlock()
	}
	return c.states()
}
func (c *collector) run(ctx context.Context) error {
	states, err := c.store.loadStates()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, len(c.workers))
	for id, w := range c.workers {
		state := states[id]
		w.mu.Lock()
		w.state.NextAt = state.NextAt
		w.state.NotBefore = state.NotBefore
		w.state.Failures = state.Failures
		w.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.runWorker(ctx, w); err != nil {
				errs <- err
				cancel()
			}
		}()
	}
	wg.Wait()
	close(errs)
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}
func (c *collector) runWorker(ctx context.Context, w *sourceWorker) error {
	for {
		w.mu.Lock()
		next := w.state.NextAt
		if w.state.NotBefore.After(next) {
			next = w.state.NotBefore
		}
		w.mu.Unlock()
		delay := next.Sub(c.now())
		if delay < 0 {
			delay = 0
		}
		if delay > w.source.Cadence {
			delay = w.source.Cadence
		}
		timer := time.NewTimer(delay)
		manual := false
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		case <-w.kick:
			manual = true
			timer.Stop()
		}
		if ctx.Err() != nil {
			return nil
		}
		now := c.now()
		w.mu.Lock()
		w.state.Queued = false
		if now.Before(w.state.NotBefore) || (!manual && now.Before(w.state.NextAt)) {
			w.mu.Unlock()
			continue
		}
		w.state.InFlightSince = &now
		select {
		case <-w.kick:
		default:
		}
		w.state.NextAt = now.Add(w.source.Cadence)
		w.state.NotBefore = time.Time{}
		if w.source.Provider == muse {
			w.state.NotBefore = w.state.NextAt
		}
		notBefore := w.state.NotBefore
		failures := w.state.Failures
		w.mu.Unlock()
		if err := c.store.reserve(w.source.ID, now.Add(w.source.Cadence), notBefore, failures); err != nil {
			return err
		}
		p := w.source.Probe(probeContext{Context: ctx, Now: c.now})
		if failure, missing := p.Outcome.(missing); ctx.Err() != nil && missing && failure.Code == "cancelled" {
			return nil
		}
		p.SourceID = w.source.ID
		p.StartedAt = now
		p.At = c.now()
		next, failures = nextProbe(p.At, w.source.Cadence, failures, p.Outcome)
		if failures > 0 || w.source.Provider == muse {
			p.NotBefore = next
		}
		if err := c.store.append(p, next, failures); err != nil {
			return err
		}
		w.mu.Lock()
		w.state.InFlightSince = nil
		w.state.NextAt = next
		w.state.NotBefore = p.NotBefore
		w.state.Failures = failures
		w.mu.Unlock()
	}
}
func nextProbe(now time.Time, interval time.Duration, failures int, outcome probeOutcome) (time.Time, int) {
	if m, failed := outcome.(missing); failed {
		if m.Code == "credential_missing" || m.Code == "credential_read" || m.Code == "credential_identity" || m.Code == "api_key" || m.Code == "unsupported_source" {
			return now.Add(interval), 0
		}
		failures++
		shift := failures - 1
		if shift > 6 {
			shift = 6
		}
		delay := interval * time.Duration(1<<shift)
		if delay > 6*time.Hour {
			delay = 6 * time.Hour
		}
		next := now.Add(delay)
		if m.RetryAt.After(next) {
			next = m.RetryAt
		}
		return next, failures
	}
	return now.Add(interval), 0
}

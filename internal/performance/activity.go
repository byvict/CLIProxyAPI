package performance

import (
	"context"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// refreshInterval bounds how long a source change waits before it reaches the page.
const refreshInterval = time.Minute

// Activity serves the latest computed Report. One goroutine recomputes it when a
// source file or the São Paulo date changes, so requests only read memory.
type Activity struct {
	t3Path, historyPath string
	start               sync.Once
	ready               chan struct{} // closed after the first attempt
	last                *stamp        // inputs of the last success, owned by the refresher

	mu     sync.Mutex
	report *Report // last success; a failure never replaces it
	err    error   // last failure, served only while report is nil
}

// stamp identifies every input of the report. An equal stamp yields an equal
// report except for GeneratedAt.
type stamp struct {
	files [4]fileStamp // t3, t3-wal, history, history-wal
	date  string       // São Paulo date: Today, midnight, and the year window
}

type fileStamp struct {
	size, modTime int64
}

// NewActivity reads nothing until the first Report call.
func NewActivity(t3Path, historyPath string) *Activity {
	return &Activity{t3Path: t3Path, historyPath: historyPath, ready: make(chan struct{})}
}

// Report returns the latest report. The first call starts the refresher and waits
// for its first attempt; a canceled request stops waiting but not the computation.
func (a *Activity) Report(ctx context.Context) (Report, error) {
	a.start.Do(func() { go a.run() })
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	select {
	case <-a.ready:
	case <-ctx.Done():
		return Report{}, ctx.Err()
	}
	return a.latest()
}

func (a *Activity) latest() (Report, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.report == nil {
		return Report{}, a.err
	}
	return *a.report, nil
}

func (a *Activity) run() {
	a.refresh(time.Now())
	close(a.ready)
	for range time.Tick(refreshInterval) {
		a.refresh(time.Now())
	}
}

// refresh recomputes when the stamp differs from the last success, so a failed
// read is retried on the next call even if no source changed.
func (a *Activity) refresh(now time.Time) {
	s := readStamp(a.t3Path, a.historyPath, now)
	if a.last != nil && *a.last == s {
		return
	}
	report, err := readActivity(context.Background(), a.t3Path, a.historyPath, now)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case err == nil:
		a.last = &s
		a.report, a.err = &report, nil
	case a.report == nil:
		a.err = err
	default:
		log.WithError(err).Warn("refresh performance activity; serving the previous report")
	}
}

// readStamp treats an empty file as missing: a read-only connection creates an
// empty -wal when none exists, and that must not trigger the next computation.
func readStamp(t3Path, historyPath string, now time.Time) stamp {
	var s stamp
	if zone, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		s.date = now.In(zone).Format(time.DateOnly)
	}
	for i, path := range []string{t3Path, t3Path + "-wal", historyPath, historyPath + "-wal"} {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			s.files[i] = fileStamp{info.Size(), info.ModTime().UnixNano()}
		}
	}
	return s
}

package submon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeLive(t *testing.T) {
	root := os.Getenv("SUBMON_LIVE_DATA_DIR")
	if root == "" {
		t.Skip("set SUBMON_LIVE_DATA_DIR to an isolated SSD directory for read-only native quota verification")
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	n, err := localAccess()
	if err != nil {
		t.Fatal(err)
	}
	client := quotaClient()
	defer client.CloseIdleConnections()
	reader := quotaReader{native: n, client: client}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	type receipt struct {
		Provider    provider      `json:"provider"`
		Status      status        `json:"status"`
		Code        string        `json:"code,omitempty"`
		Basis       identityBasis `json:"identityBasis,omitempty"`
		Windows     int           `json:"windowCount"`
		Cached      bool          `json:"cached"`
		SideEffects []string      `json:"sideEffects,omitempty"`
	}
	receipts := []receipt{}
	sources := []source{}
	results := make(chan receipt, len(providers))
	now := time.Now()
	for _, src := range reader.sources() {
		if state := data.Sources[src.ID]; state.NextAt.After(now) {
			r := receipt{Provider: src.Provider, Cached: true, Status: unavailable}
			for _, a := range data.Attempts {
				if a.SourceID == src.ID {
					r.Status = a.Status
					r.Code = a.Code
				}
			}
			receipts = append(receipts, r)
			continue
		}
		original := src.Probe
		p := src.Provider
		src.Probe = func(c probeContext) probe {
			result := original(c)
			r := receipt{Provider: p, SideEffects: result.SideEffects}
			switch o := result.Outcome.(type) {
			case reading:
				r.Status = ok
				r.Basis = o.Identity.Basis
				r.Windows = len(o.Windows)
			case missing:
				r.Status = o.Status
				r.Code = o.Code
			}
			results <- r
			return result
		}
		sources = append(sources, src)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() { completed <- newCollector(s, sources, time.Now).run(ctx) }()
	for range sources {
		r := <-results
		receipts = append(receipts, r)
	}
	cancel()
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(struct {
		CheckedAt                 time.Time `json:"checkedAt"`
		NativeCredentialsModified bool      `json:"nativeCredentialsModified"`
		InferenceRequests         bool      `json:"inferenceRequests"`
		Providers                 []receipt `json:"providers"`
	}{CheckedAt: time.Now().In(saoPaulo), Providers: receipts}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "live-receipt.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(b))
}

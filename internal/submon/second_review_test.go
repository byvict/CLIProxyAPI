package submon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHistoryQueriesSearchArchivedTables(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for day := 0; day < 100; day++ {
		p := successfulProbe(testTime().Add(time.Duration(day)*24*time.Hour), "person")
		if err = s.append(p, p.At, 0); err != nil {
			t.Fatal(err)
		}
	}
	from := testTime().Add(98 * 24 * time.Hour)
	readings, attempts := historyQueries(from)
	for _, query := range []string{readings, attempts} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Log(detail)
			fields := strings.Fields(detail)
			if len(fields) > 1 && fields[0] == "SCAN" {
				for _, table := range []string{"attempts", "readings", "r", "a", "latest", "previous"} {
					if fields[1] == table {
						t.Error("archived table scanned", detail)
					}
				}
			}
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	bounded, err := s.loadSince(from)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Attempts) != 3 || len(bounded.Samples) != 3 {
		t.Fatal("range/head UNION lost deduplication")
	}
	for i, actual := range bounded.Samples {
		want, _ := json.Marshal(full.Samples[97+i])
		got, _ := json.Marshal(actual)
		if string(want) != string(got) {
			t.Fatal("bounded row differs from retained row")
		}
	}
}

func TestPeriodBoundaryContextPreservesCompletedCycle(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	start := testTime()
	for i, at := range []time.Time{start.Add(-5 * time.Minute), start.Add(5 * time.Minute)} {
		p := successfulProbe(at, "person")
		r := p.Outcome.(reading)
		reset := start.Add(time.Duration(i) * 7 * 24 * time.Hour)
		r.Windows[0].ResetsAt = &reset
		r.Windows[0].Remaining = number(40 + float64(i)*60)
		p.Outcome = r
		if err = s.append(p, p.At, 0); err != nil {
			t.Fatal(err)
		}
	}
	now := start.Add(24 * time.Hour)
	h := newAPI(s, newCollector(s, nil, func() time.Time { return now }), "fixture", func() time.Time { return now }, "127.0.0.1:8318")
	w := requestAPI(h, "GET", "/api/v1/view?provider=codex&window=weekly&period=24h", "", "fixture", "", "127.0.0.1:8318", nil)
	var v view
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Groups) != 1 {
		t.Fatal("view unavailable", w.Code)
	}
	a := v.Groups[0].Accounts[0]
	if len(a.UnusedCycles) != 1 {
		t.Fatal("boundary reset lost pre-period context")
	}
	for _, p := range a.Observed {
		if p.At.Before(start) {
			t.Fatal("out-of-period chart point")
		}
	}
}

func TestOptionalCodexContainersPreserveMainWindows(t *testing.T) {
	base := `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":604800}}`
	for _, tail := range []string{
		`,"additional_rate_limits":[{"limit_name":"spark","rate_limit":null}]}`,
		`,"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":null,"secondary_window":null}}]}`,
		`,"code_review_rate_limit":{"primary_window":null,"secondary_window":null}}`,
		`,"code_review_rate_limits":{}}`,
	} {
		windows, err := parseWindows(codex, []byte(base+tail))
		if err != nil || len(windows) != 1 {
			t.Fatal("optional empty set discarded main quota", err, windows)
		}
	}
	for _, tail := range []string{
		`,"additional_rate_limits":[{"limit_name":"spark","primary_window":{"used_percent":100,"limit_window_seconds":18000}}]}`,
		`,"code_review_rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":604800}}}`,
	} {
		windows, err := parseWindows(codex, []byte(base+tail))
		if err != nil || len(windows) != 2 || availabilityOf(windows).State != "limited" {
			t.Fatal("HTTP scoped limit lost", err, windows)
		}
	}
	windows, err := parseWindows(codex, []byte(base+`,"additional_rate_limits":[{"limit_name":"spark","primary":{"window_minutes":300,"used_percent":20}}]}`))
	if err != nil || len(windows) != 2 || availabilityOf(windows).State != "unknown" {
		t.Fatal("unrecognized scope was not retained as unknown", err, windows)
	}
	if _, err = parseWindows(codex, []byte(base+`,"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":150,"limit_window_seconds":18000}}}]}`)); err == nil {
		t.Fatal("invalid numeric limit became real balance")
	}
}

func TestCodexMetadataPersistsWithoutCreatingConsumptionWindows(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	details := parseCodexDetails([]byte(`{"rate_limit_reset_credits":{"available_count":0},"model_usage":{"fixture-model":{"available":false,"available_at":null}},"api_key":"fixture-secret-not-retained"}`))
	for i := 0; i < 3; i++ {
		p := successfulProbe(testTime().Add(time.Duration(i)*5*time.Minute), "person")
		r := p.Outcome.(reading)
		r.Details = details
		reset := testTime().Add(24 * time.Hour)
		r.Windows[0].ResetsAt = &reset
		p.Outcome = r
		if err = s.append(p, p.At, 0); err != nil {
			t.Fatal(err)
		}
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	a := makeSnapshot(data, nil, testTime().Add(10*time.Minute)).Accounts[0]
	if a.ResetCredits == nil || *a.ResetCredits != 0 || len(a.ModelAvailability) != 1 || a.Availability.State != "limited" || len(a.Windows) != 1 || a.ResetAction.Enabled {
		t.Fatal("read-only metadata was lost or became a quota window", a)
	}
	if a.Windows[0].PacePpH == nil || *a.Windows[0].PacePpH != 0 {
		t.Fatal("binary model availability created consumption")
	}
	b, _ := json.Marshal(data)
	if strings.Contains(string(b), "fixture-secret-not-retained") {
		t.Fatal("raw metadata persisted")
	}
}

func TestSnapshotUsesPastWindowValue(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for i := 0; i < 2; i++ {
		p := successfulProbe(testTime().Add(time.Duration(i)*10*time.Minute), "person")
		r := p.Outcome.(reading)
		r.Windows[0].Remaining = number(80 - float64(i)*60)
		p.Outcome = r
		if err = s.append(p, p.At, 0); err != nil {
			t.Fatal(err)
		}
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	a := makeSnapshot(data, nil, testTime()).Accounts[0]
	if len(a.Windows) != 1 || *a.Windows[0].Remaining != 80 {
		t.Fatal("future value shown as current balance")
	}
}

func TestLocalCredentialFailuresDoNotReserveRemoteBackoff(t *testing.T) {
	for _, code := range []string{"credential_read", "credential_identity"} {
		next, failures := nextProbe(testTime(), 5*time.Minute, 20, missing{Status: invalidResponse, Code: code})
		if failures != 0 || !next.Equal(testTime().Add(5*time.Minute)) {
			t.Fatal("local failure reserved remote backoff", code)
		}
	}
}

func TestShutdownKeepsCompletedExchange(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := source{ID: "muse-native", Provider: muse, Cadence: 30 * time.Minute, Probe: func(c probeContext) probe {
		p := successfulProbe(c.Now(), "person")
		p.SideEffects = []string{"credential_exchange_requested"}
		cancel()
		return p
	}}
	c := newCollector(s, []source{src}, testTime)
	if err = c.run(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil || len(data.Attempts) != 1 || len(data.Attempts[0].SideEffects) != 1 {
		t.Fatal("completed exchange lost during shutdown", err)
	}
}

func TestScheduledProbeConsumesQueuedRefresh(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	src := source{ID: "codex-native", Provider: codex, Cadence: 5 * time.Minute, Probe: func(c probeContext) probe {
		close(entered)
		<-c.Context.Done()
		return probe{Outcome: missing{Status: transportFailure, Code: "cancelled"}}
	}}
	c := newCollector(s, []source{src}, testTime)
	c.refresh(src.ID)
	done := make(chan error, 1)
	go func() { done <- c.runWorker(ctx, c.workers[src.ID]) }()
	<-entered
	if len(c.workers[src.ID].kick) != 0 {
		t.Error("scheduled probe left a stale manual request")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

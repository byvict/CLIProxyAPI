package submon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResetJitterPreservesCycleAndForecast(t *testing.T) {
	samples := history(90, 89, 88, 87, 86, 85, 84, 83, 82, 81, 80, 79, 78)
	for i := range samples {
		reset := samples[i].Window.ResetsAt.Add(time.Duration(i%3-1) * 700 * time.Millisecond)
		samples[i].Window.ResetsAt = &reset
	}
	a := analyze(samples, analysisOptions{Now: samples[12].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour})
	if len(a.Intervals) != 12 || a.RecentPpH == nil || a.Peak == nil || len(a.Forecast) == 0 {
		t.Fatalf("reset jitter discarded the current cycle: intervals=%d pace=%v forecast=%d", len(a.Intervals), a.RecentPpH, len(a.Forecast))
	}
}

func TestVerifiedMuseSwitchWithoutQuota(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := successfulProbe(testTime(), "previous")
	p.SourceID = "muse-native"
	r := p.Outcome.(reading)
	r.Identity = identity{Provider: muse, Basis: providerEmail, Subject: "old@example.test", Email: "old@example.test"}
	p.Outcome = r
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	p.At = p.At.Add(30 * time.Minute)
	p.StartedAt = p.At
	p.Outcome = parseMuse([]byte(`{"user_email":"new@example.test","subs_usage":null}`))
	if err = s.append(p, p.At, 1); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	snap := makeSnapshot(data, nil, p.At)
	if len(snap.Accounts) != 2 {
		t.Fatal("verified new principal was discarded")
	}
	active := 0
	for _, a := range snap.Accounts {
		if a.Active {
			active++
		}
		if a.ID == r.Identity.accountID() && a.Active {
			t.Fatal("old principal remains current")
		}
		if a.Active && (len(a.Windows) != 0 || a.Status != string(unavailable)) {
			t.Fatal("new principal received invented quota")
		}
	}
	if active != 1 {
		t.Fatal("new principal was not bound exactly once")
	}
}

func TestClaudeTransientProfileFailureKeepsBinding(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	n := nativeAccess{getenv: func(key string) string {
		if key == "CLAUDE_CODE_OAUTH_TOKEN" {
			return "fixture"
		}
		return ""
	}}
	failProfile := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		code, body := 200, string(fixture(t, "claude.json"))
		if strings.HasSuffix(req.URL.Path, "profile") {
			body = string(fixture(t, "claude-profile.json"))
			if failProfile {
				code, body = 429, `{}`
			}
		}
		return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	reader := quotaReader{native: n, client: client}
	for i := 0; i < 2; i++ {
		at := testTime().Add(time.Duration(i) * 5 * time.Minute)
		p := reader.probe(probeContext{Context: context.Background(), Now: func() time.Time { return at }}, claude)
		p.SourceID = "claude-native"
		if err = s.append(p, at.Add(5*time.Minute), i); err != nil {
			t.Fatal(err)
		}
		failProfile = true
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	snap := makeSnapshot(data, nil, testTime().Add(5*time.Minute))
	if !snap.Accounts[0].Active || snap.Accounts[0].Status != string(rateLimited) || snap.Accounts[0].Freshness != "stale" {
		t.Fatal("transient profile failure signed the account out", snap.Accounts[0])
	}
}

func TestNullWindowPreservesOtherCurrentWindows(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := successfulProbe(testTime(), "person")
	r := p.Outcome.(reading)
	r.Windows = append(r.Windows, testWindow("session", 70))
	r.Windows[0].Remaining = nil
	p.Outcome = r
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	snap := makeSnapshot(data, nil, p.At)
	u := usageSnapshot(snap, p.At)
	if snap.Accounts[0].Freshness != "fresh" || u.Providers[0].Status != "ok" || len(u.Providers[0].Windows) != 2 {
		t.Fatal("null balance hid current windows", u.Providers[0])
	}
	if snap.Accounts[0].Availability.State != "unknown" {
		t.Fatal("unknown limit became available")
	}
}

func TestMalformedMuseIsNotDormant(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(fixture(t, "muse.json"), &raw); err != nil {
		t.Fatal(err)
	}
	usage := raw["subs_usage"].(map[string]any)
	usage["window"].(map[string]any)["used_percent"] = 150
	data, _ := json.Marshal(raw)
	m, valid := parseMuse(data).(missing)
	if !valid || m.Status != invalidResponse || m.Code == "dormant" {
		t.Fatal("malformed data described as dormancy", m)
	}
}

func TestFutureSamplesDoNotDriveAnalysis(t *testing.T) {
	samples := history(90, 89, 88, 20)
	now := samples[2].At
	a := analyze(samples, analysisOptions{Now: now, Cadence: 5 * time.Minute, Period: time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if len(a.Forecast) == 0 || a.Forecast[0].Remaining != 88 {
		t.Fatal("future reading drove forecast", a.Forecast)
	}
}

func TestRetryAfterStartsAtResponseAndRejectsAbsurdDates(t *testing.T) {
	now := testTime()
	r := quotaReader{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		now = now.Add(45 * time.Minute)
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"3600"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}}
	_, absent := r.request(context.Background(), func() time.Time { return now }, "https://fixture.test/quota", "fixture", nil, false)
	if absent == nil || !absent.RetryAt.Equal(now.Add(time.Hour)) {
		t.Fatal("latency shortened Retry-After", absent)
	}
	if !retryAfter(now.AddDate(5, 0, 0).Format(http.TimeFormat), now).IsZero() {
		t.Fatal("implausible date accepted")
	}
}

func TestManualRefreshSkipsScheduleButRespectsConstraints(t *testing.T) {
	for _, provider := range []provider{codex, muse} {
		t.Run(string(provider), func(t *testing.T) {
			s, err := openStore(testDir(t))
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			entered := make(chan struct{})
			src := source{ID: string(provider) + "-native", Provider: provider, Cadence: cadence(provider), Probe: func(c probeContext) probe {
				close(entered)
				<-c.Context.Done()
				return probe{Outcome: missing{Status: transportFailure}}
			}}
			c := newCollector(s, []source{src}, testTime)
			worker := c.workers[src.ID]
			worker.state.NextAt = testTime().Add(cadence(provider))
			if provider == muse {
				worker.state.NotBefore = worker.state.NextAt
			}
			h := newAPI(s, c, "fixture-key", testTime, "127.0.0.1:8318")
			w := requestAPI(h, "POST", "/api/v1/refresh", `{"source":"`+src.ID+`"}`, "fixture-key", "", "127.0.0.1:8318", nil)
			if w.Code != 202 {
				t.Fatal("refresh failed", w.Code)
			}
			queued := c.states()[src.ID].Queued
			if provider == muse {
				if queued || len(worker.kick) != 0 {
					t.Fatal("mint floor bypassed")
				}
				return
			}
			if !queued {
				t.Fatal("manual refresh did not advance ordinary schedule")
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- c.runWorker(ctx, worker) }()
			<-entered
			cancel()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			data, err := s.load()
			if err != nil || len(data.Attempts) != 0 {
				t.Fatal("shutdown recorded as provider failure")
			}
			worker.mu.Lock()
			worker.state.InFlightSince = nil
			worker.state.NotBefore = testTime().Add(time.Hour)
			worker.mu.Unlock()
			if c.refresh(src.ID)[src.ID].Queued {
				t.Fatal("provider backoff bypassed")
			}
		})
	}
	if next, failures := nextProbe(testTime(), 5*time.Minute, 20, missing{Status: unavailable, Code: "credential_missing"}); failures != 0 || !next.Equal(testTime().Add(5*time.Minute)) {
		t.Fatal("local logout accumulated remote backoff")
	}
}

func TestBoundedStorePreservesWindowHeadsAndMigrates(t *testing.T) {
	dir := testDir(t)
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for day := 0; day < 40; day++ {
		p := successfulProbe(testTime().Add(time.Duration(day)*24*time.Hour), "person")
		if day > 10 {
			r := p.Outcome.(reading)
			r.Windows[0].Key.CapacityBasis = "new-plan"
			p.Outcome = r
		}
		if err = s.append(p, p.At.Add(time.Hour), 0); err != nil {
			t.Fatal(err)
		}
	}
	last := testTime().Add(39 * 24 * time.Hour)
	if err = s.reserve("codex-native", last.Add(time.Hour), last.Add(30*time.Minute), 2); err != nil {
		t.Fatal(err)
	}
	data, err := s.loadSince(last.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Samples) != 3 || len(data.Attempts) != 3 || len(makeSnapshot(data, nil, last).Accounts[0].HistoryWindows) != 2 {
		t.Fatal("bounded read lost metadata or loaded archived series", len(data.Samples), len(data.Attempts))
	}
	if _, err = s.db.Exec("DROP TABLE window_heads; DROP INDEX reading_time; DROP INDEX attempt_time; ALTER TABLE source_states DROP COLUMN not_before_ns; PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	if err = s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(filepath.Clean(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	data, err = s.loadSince(last.Add(-24 * time.Hour))
	if err != nil || len(data.Samples) != 3 {
		t.Fatal("migration lost history", err)
	}
	states, err := s.loadStates()
	if err != nil || !states["codex-native"].NotBefore.Equal(last.Add(time.Hour)) {
		t.Fatal("migration weakened old reservation")
	}
}

func TestAdditionalCodexAndUnknownClaudeLimits(t *testing.T) {
	body := `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}},"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":18000}}}],"code_review_rate_limits":{"primary_window":{"used_percent":80,"limit_window_seconds":604800}}}`
	windows, err := parseWindows(codex, []byte(body))
	if err != nil || len(windows) != 3 || availabilityOf(windows).State != "limited" {
		t.Fatal("scoped Codex limit lost", err, windows)
	}
	if _, err = parseWindows(claude, []byte(`{"limits":[{"kind":"session","percent":20},{"kind":"daily","percent":100}]}`)); err == nil {
		t.Fatal("unknown Claude limit silently omitted")
	}
}

func TestUsageReportsSourceErrorsAndSideEffectsWithoutAccount(t *testing.T) {
	at := testTime()
	snap := snapshot{Sources: []sourceView{{Provider: muse, Status: authFailure, LastAttemptAt: &at, SideEffects: []string{"credential_exchange_requested"}}}}
	u := usageSnapshot(snap, at.Add(time.Hour))
	if u.Providers[0].Status != "error" || len(u.Providers[0].SideEffects) != 1 || !u.CheckedAt.Equal(at) {
		t.Fatal("provider failure or exchange metadata lost", u)
	}
	empty := usageSnapshot(snapshot{}, at)
	if empty.CheckedAt.Unix() != 0 || empty.CheckedAtBasis != "none" || !empty.GeneratedAt.Equal(at) {
		t.Fatal("generation presented as observation")
	}
}

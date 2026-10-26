package submon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSnapshotPrivacyFreshnessAndCurrentNativeAccount(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := testTime()
	p := successfulProbe(now, "old")
	if err = s.append(p, now, 0); err != nil {
		t.Fatal(err)
	}
	p = successfulProbe(now.Add(5*time.Minute), "new")
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := makeSnapshot(data, nil, now.Add(5*time.Minute))
	b, _ := json.Marshal(snapshot)
	if strings.Contains(string(b), "fixture-private@example.test") || strings.Contains(string(b), `"email"`) {
		t.Fatal("email in snapshot")
	}
	if len(snapshot.Accounts) != 2 {
		t.Fatal("lost prior history")
	}
	compat := usageSnapshot(snapshot, now.Add(5*time.Minute))
	if compat.Providers[0].Status != "ok" || len(compat.Providers[0].Windows) != 1 {
		t.Fatalf("compat %+v", compat)
	}
	if !compat.CheckedAt.Equal(p.At) {
		t.Fatal("old account used as current", compat.CheckedAt)
	}
	stale := usageSnapshot(makeSnapshot(data, nil, now.Add(time.Hour)), now.Add(time.Hour))
	if stale.Providers[0].Status == "ok" {
		t.Fatal("stale returned as fresh")
	}
	p.At = p.At.Add(time.Minute)
	p.StartedAt = p.At
	p.Outcome = missing{Status: authFailure}
	if err = s.append(p, p.At.Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	data, err = s.load()
	if err != nil {
		t.Fatal(err)
	}
	snapshot = makeSnapshot(data, nil, p.At)
	for _, a := range snapshot.Accounts {
		if a.Active && (a.Windows[0].Remaining == nil || a.Freshness != "stale") {
			t.Fatal("last good not retained honestly", a)
		}
	}
}
func TestAllSelectionAndCompatibleTotals(t *testing.T) {
	data := storedData{Accounts: []storedAccount{}, Sources: map[string]sourceState{}}
	v, err := makeView(data, nil, testTime(), viewSelection{Provider: "all", Window: "weekly", Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour})
	if err != nil || len(v.Groups) != 0 || v.SelectionRequired == "" {
		t.Fatal("All silently chose provider")
	}
}
func TestRateDoesNotBorrowPreviousCycle(t *testing.T) {
	s := history(90, 80, 70, 60, 50)
	reset := s[2].At.Add(time.Minute)
	nextReset := reset.Add(7 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		s[i].Window.ResetsAt = &reset
	}
	for i := 3; i < 5; i++ {
		s[i].Window.ResetsAt = &nextReset
		s[i].Window.Remaining = number(100 - float64(i-3))
	}
	a := analyze(s, analysisOptions{Now: s[4].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if a.RecentPpH != nil {
		t.Fatal("previous cycle supplied insufficient current history")
	}
	s = append(s, sample{At: s[4].At.Add(5 * time.Minute), Window: s[4].Window})
	s[5].Window.Remaining = number(98)
	a = analyze(s, analysisOptions{Now: s[5].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if a.RecentPpH == nil || *a.RecentPpH > 12.01 {
		t.Fatal("previous cycle polluted current rate", a.RecentPpH)
	}
}

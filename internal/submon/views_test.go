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

func TestHistoryCannotCrossInactiveSourceBinding(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	first := successfulProbe(testTime(), "a")
	middle := successfulProbe(testTime().Add(5*time.Minute), "b")
	last := successfulProbe(testTime().Add(10*time.Minute), "a")
	reset := testTime().Add(48 * time.Hour)
	for _, p := range []*probe{&first, &middle, &last} {
		r := p.Outcome.(reading)
		r.Windows[0].ResetsAt = &reset
		p.Outcome = r
	}
	for _, p := range []probe{first, middle, last} {
		if err = s.append(p, p.At, 0); err != nil {
			t.Fatal(err)
		}
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	r := first.Outcome.(reading)
	h := historyFor(data, r.Identity.accountID(), r.Windows[0].Key)
	a := analyze(h, analysisOptions{Now: last.At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour})
	if len(a.Intervals) != 0 {
		t.Fatal("delta crossed signed-out interval")
	}
}

func TestHistoricalWindowRemainsSelectableAfterCapacityChange(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := successfulProbe(testTime(), "a")
	r := p.Outcome.(reading)
	old := r.Windows[0].Key
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	p.At = p.At.Add(5 * time.Minute)
	p.StartedAt = p.At
	r.Windows[0].Key.CapacityBasis = "new-plan"
	p.Outcome = r
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	snap := makeSnapshot(data, nil, p.At)
	if len(snap.Accounts[0].HistoryWindows) != 2 || len(snap.Accounts[0].Windows) != 1 {
		t.Fatal("historical window lost")
	}
	v, err := makeView(data, nil, p.At, viewSelection{Provider: "codex", Account: r.Identity.accountID(), Window: old.id(), Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if err != nil || len(v.Groups) != 1 || len(v.Groups[0].Accounts[0].Observed) == 0 {
		t.Fatal("historical window inaccessible")
	}
}

func TestUsageCheckedAtUsesOldestContributingObservation(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	first := successfulProbe(testTime(), "codex-person")
	second := successfulProbe(testTime().Add(time.Minute), "grok-person")
	second.SourceID = "grok-native"
	r := second.Outcome.(reading)
	r.Identity.Provider = grok
	second.Outcome = r
	for _, p := range []probe{first, second} {
		if err = s.append(p, p.At, 0); err != nil {
			t.Fatal(err)
		}
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	report := usageSnapshot(makeSnapshot(data, nil, second.At), second.At)
	if !report.CheckedAt.Equal(first.At) {
		t.Fatal("shared timestamp hid older provider observation")
	}
	if !report.Providers[2].CheckedAt.Equal(second.At) {
		t.Fatal("provider timestamp lost")
	}
}

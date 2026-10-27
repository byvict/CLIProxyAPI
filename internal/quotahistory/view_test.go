package quotahistory

import (
	"testing"
	"time"
)

func TestV1HistoryViewRetainsChartsAndIndependentAccounts(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	reset := now.Add(24 * time.Hour).UnixMilli()
	period := 168.0
	observations := []Observation{}
	for i := 0; i <= 24; i++ {
		balance := 50.0 - float64(i)/12
		observations = append(observations, Observation{Credential: "account-a", Provider: "claude", WindowID: "seven-day", Label: "7-day limit", ObservedAtMs: now.Add(time.Duration(i-24) * 5 * time.Minute).UnixMilli(), Remaining: &balance, ResetAtMs: &reset, PeriodHours: &period})
	}
	credentials := []Credential{{ID: "account-a", Provider: "claude", Label: "Claude A", Active: true}, {ID: "account-b", Provider: "codex", Label: "Codex B", Active: true}}
	snapshot := MakeSnapshot(observations, credentials, now)
	if len(snapshot.Accounts) != 2 || len(snapshot.Providers) != 2 {
		t.Fatalf("missing registered credentials: %+v", snapshot)
	}
	var windowID string
	for _, account := range snapshot.Accounts {
		if account.ID == "account-a" {
			windowID = account.Windows[0].ID
		}
	}
	view := MakeView(observations, snapshot, "claude", "", windowID, 24*time.Hour, now)
	if len(view.Groups) != 1 || len(view.Groups[0].Accounts) != 1 {
		t.Fatalf("missing v1 history group: %+v", view)
	}
	a := view.Groups[0].Accounts[0]
	if len(a.Rate) == 0 || len(a.Hourly) != 24 || len(a.Heatmap) != 168 || a.RecentPpH == nil || *a.RecentPpH < .999 || *a.RecentPpH > 1.001 {
		t.Fatalf("missing v1 chart data: rates %d, bins %d, heatmap %d, rate %v", len(a.Rate), len(a.Hourly), len(a.Heatmap), a.RecentPpH)
	}
	if MakeView(observations, snapshot, "all", "", "weekly", time.Hour, now).SelectionRequired == "" {
		t.Fatal("the v1 overview must ask for an explicit provider selection")
	}
	if got := MakeView(observations, snapshot, "codex", "", windowID, time.Hour, now); len(got.Groups) != 0 {
		t.Fatal("history was assigned to a different provider")
	}
}

func TestV1HistoryDoesNotCountCrossHourConsumptionTwice(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	reset := now.Add(24 * time.Hour).UnixMilli()
	period := 168.0
	first, last := 51.0, 50.0
	observations := []Observation{
		{Credential: "a", Provider: "claude", WindowID: "seven-day", ObservedAtMs: now.Add(-5 * time.Minute).UnixMilli(), Remaining: &first, ResetAtMs: &reset, PeriodHours: &period},
		{Credential: "a", Provider: "claude", WindowID: "seven-day", ObservedAtMs: now.Add(5 * time.Minute).UnixMilli(), Remaining: &last, ResetAtMs: &reset, PeriodHours: &period},
	}
	snapshot := MakeSnapshot(observations, []Credential{{ID: "a", Provider: "claude", Active: true}}, now.Add(10*time.Minute))
	view := MakeView(observations, snapshot, "claude", "", snapshot.Accounts[0].Windows[0].ID, time.Hour, now.Add(10*time.Minute))
	a := view.Groups[0].Accounts[0]
	if a.UnattributedPp != 1 {
		t.Fatalf("unassigned consumption = %v, want 1", a.UnattributedPp)
	}
	for _, bin := range a.Hourly {
		if bin.ConsumedPp != 0 {
			t.Fatalf("cross-hour interval counted in bin: %+v", bin)
		}
	}
}

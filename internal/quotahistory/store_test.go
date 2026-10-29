package quotahistory

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHistorySurvivesReopenAndKeepsIndependentWindows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	remaining := 70.0
	reset := int64(1000)
	period := 168.0
	observations := []Observation{
		{Credential: "a", Provider: "claude", WindowID: "weekly", ObservedAtMs: 100, Remaining: &remaining, ResetAtMs: &reset, PeriodHours: &period},
		{Credential: "a", Provider: "claude", WindowID: "weekly", ObservedAtMs: 200, Remaining: &remaining},
		{Credential: "a", Provider: "claude", WindowID: "session", ObservedAtMs: 200},
		{Credential: "b", Provider: "claude", WindowID: "weekly", ObservedAtMs: 200, Remaining: &remaining},
		{Credential: "a", Provider: "claude", WindowID: "weekly", ObservedAtMs: 300, Remaining: &remaining},
	}
	for i := 0; i < 2; i++ {
		if err := Record(ctx, path, observations); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Read(ctx, path, 150, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d observations, want 3 in range plus one predecessor", len(got))
	}
	for _, observation := range got {
		if observation.ObservedAtMs > 250 {
			t.Fatalf("read future observation: %+v", observation)
		}
		if observation.WindowID == "session" && observation.Remaining != nil {
			t.Fatal("unknown balance was converted into a number")
		}
		if observation.ObservedAtMs == 100 && (observation.ResetAtMs == nil || *observation.ResetAtMs != reset || observation.PeriodHours == nil || *observation.PeriodHours != period) {
			t.Fatalf("lost quota metadata: %+v", observation)
		}
	}
}

func TestPollCadenceSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	for _, step := range []struct {
		credential string
		now        int64
		want       bool
	}{
		{"muse", 1000, true},
		{"muse", 1000, false},
		{"muse", 1000 + 30*60*1000 - 1, false},
		{"other", 1001, true},
		{"muse", 1000 + 30*60*1000, true},
	} {
		claimed, err := ReservePoll(context.Background(), path, step.credential, step.now, 30*60*1000, false)
		if err != nil || claimed != step.want {
			t.Fatalf("reserve %s at %d: claimed %v, want %v, error %v", step.credential, step.now, claimed, step.want, err)
		}
	}
}

func TestPeriodReadPreservesSnapshotAndView(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	reset := now.Add(24 * time.Hour).UnixMilli()
	observations := []Observation{}
	for _, credential := range []string{"active", "historical"} {
		for _, hours := range []float64{5, 168, 720} {
			for i := -40 * 48; i <= 0; i++ {
				if hours != 168 && i > -32*48 {
					continue
				}
				balance := 50 - float64(i)/1000
				period := hours
				provider := "claude"
				if credential == "historical" && i < -35*48 {
					provider = "legacy-provider"
				}
				observations = append(observations, Observation{Credential: credential, Provider: provider, WindowID: "window", Label: "Window", ObservedAtMs: now.Add(time.Duration(i)*30*time.Minute).UnixMilli() + int64(hours), Remaining: &balance, ResetAtMs: &reset, PeriodHours: &period})
			}
		}
	}
	if err := Record(ctx, path, observations); err != nil {
		t.Fatal(err)
	}
	full, err := Read(ctx, path, 0, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	credentials := []Credential{{ID: "active", Provider: "claude", Active: true}}
	snapshot := MakeSnapshot(full, credentials, now)
	for _, period := range []time.Duration{0, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour} {
		viewRows, metadata, err := ReadView(ctx, path, now.Add(-period).UnixMilli(), now.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		direct, err := Read(ctx, path, now.Add(-period).UnixMilli(), now.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(direct, viewRows) {
			t.Fatal("identity metadata leaked into analysis")
		}
		gotSnapshot := MakeSnapshot(metadata, credentials, now)
		if !reflect.DeepEqual(snapshot, gotSnapshot) {
			t.Fatalf("snapshot lost historical identities for %v", period)
		}
		if period == 0 {
			continue
		}
		for _, account := range snapshot.Accounts {
			for _, window := range account.HistoryWindows {
				want := MakeView(full, snapshot, account.Provider, account.ID, window.ID, period, now)
				got := MakeView(viewRows, gotSnapshot, account.Provider, account.ID, window.ID, period, now)
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("limited view differs for %v", period)
				}
			}
		}
	}
}

func TestBackgroundCollectionTakesOverWhenPanelStopsPolling(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	minute := int64(60_000)
	steps := []struct {
		at                  int64
		background, claimed bool
	}{
		{0, false, true}, {30, true, false}, {30, false, true}, {60, true, false},
		{65, true, true}, {66, true, false}, {67, false, false}, {97, false, true},
	}
	for _, s := range steps {
		got, err := ReservePoll(ctx, path, "account", s.at*minute, 30*minute, s.background)
		if err != nil || got != s.claimed {
			t.Fatalf("at %d background %v: %v %v", s.at, s.background, got, err)
		}
	}
}

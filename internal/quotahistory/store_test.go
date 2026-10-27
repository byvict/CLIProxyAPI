package quotahistory

import (
	"context"
	"path/filepath"
	"testing"
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
		claimed, err := ReservePoll(context.Background(), path, step.credential, step.now, 30*60*1000)
		if err != nil || claimed != step.want {
			t.Fatalf("reserve %s at %d: claimed %v, want %v, error %v", step.credential, step.now, claimed, step.want, err)
		}
	}
}

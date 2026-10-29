package quotahistory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRekeyPreservesRowsReservationsAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	remaining := 70.0
	row := Observation{Credential: "old", Provider: "codex", WindowID: "weekly", ObservedAtMs: 100, Remaining: &remaining}
	if err := Record(ctx, path, []Observation{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReservePoll(ctx, path, "old", 100, 1000, false); err != nil {
		t.Fatal(err)
	}
	aliases := map[string]string{"old": "stable"}
	for i := 0; i < 2; i++ {
		if err := Rekey(ctx, path, aliases); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := Read(ctx, path, 0, 1000)
	if err != nil || len(rows) != 1 || rows[0].Credential != "stable" || *rows[0].Remaining != 70 {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	if claimed, err := ReservePoll(ctx, path, "stable", 200, 1000, false); err != nil || claimed {
		t.Fatalf("lost reservation: %v %v", claimed, err)
	}
	remaining = 50
	if err := Record(ctx, path, []Observation{row}); err != nil {
		t.Fatal(err)
	}
	if err := Rekey(ctx, path, aliases); err == nil {
		t.Fatal("conflicting history silently replaced")
	}
	rows, err = Read(ctx, path, 0, 1000)
	if err != nil || len(rows) != 2 {
		t.Fatalf("conflict changed history: %+v %v", rows, err)
	}
}

func TestConcurrentRekeyAndPolling(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history ?#.sqlite")
	remaining := 50.0
	if err := Record(ctx, path, []Observation{{Credential: "old", Provider: "codex", WindowID: "weekly", ObservedAtMs: 100, Remaining: &remaining}}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			if err := Rekey(ctx, path, map[string]string{"old": "stable"}); err != nil {
				results <- err
				return
			}
			_, err := ReservePoll(ctx, path, "stable", 200, 1000, true)
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent collection: %v", err)
		}
	}
	rows, err := Read(ctx, path, 0, 1000)
	if err != nil || len(rows) != 1 || rows[0].Credential != "stable" {
		t.Fatalf("history: %+v %v", rows, err)
	}
}

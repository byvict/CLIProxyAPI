package quotahistory

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestSharedGapBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../management/tests/fixtures/quota-gaps.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Provider string
		GapMs    int64
		Accepted bool
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	remaining := 60.0
	reset := now.Add(24 * time.Hour)
	hours := 168.0
	for _, c := range cases {
		t.Run(c.Provider+(time.Duration(c.GapMs)*time.Millisecond).String(), func(t *testing.T) {
			observation := Observation{Credential: "a", Provider: c.Provider, WindowID: "weekly", ObservedAtMs: now.UnixMilli(), Remaining: &remaining, PeriodHours: &hours}
			a := asSample(observation)
			a.Window.ResetsAt = &reset
			b := a
			b.At = now.Add(time.Duration(c.GapMs) * time.Millisecond)
			if continuous(a, b, maxGapFor(c.Provider)) != c.Accepted {
				t.Fatal("gap boundary")
			}
			snapshot := MakeSnapshot([]Observation{observation}, nil, b.At)
			if (snapshot.Accounts[0].Windows[0].Freshness == "fresh") != c.Accepted {
				t.Fatal("freshness boundary")
			}
			missing := a
			missing.Window.Remaining = nil
			if continuous(missing, b, maxGapFor(c.Provider)) {
				t.Fatal("null bridge")
			}
			changedReset := reset.Add(2 * time.Minute)
			b.Window.ResetsAt = &changedReset
			if continuous(a, b, maxGapFor(c.Provider)) {
				t.Fatal("reset bridge")
			}
			b.Window.ResetsAt = &reset
			expired := a
			expired.Window.ResetsAt = &b.At
			b.Window.ResetsAt = &b.At
			if continuous(expired, b, maxGapFor(c.Provider)) {
				t.Fatal("expired reset bridge")
			}
			b.Window.ResetsAt = &reset
			increased := 61.0
			b.Window.Remaining = &increased
			if continuous(a, b, maxGapFor(c.Provider)) {
				t.Fatal("increase bridge")
			}
		})
	}
}
func TestThirtyMinuteReadingsAndFutureBarrier(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	reset := now.Add(24 * time.Hour)
	samples := []sample{}
	for i := 0; i < 4; i++ {
		v := 60 - float64(i)
		samples = append(samples, sample{At: now.Add(time.Duration(i-2) * 30 * time.Minute), Window: windowReading{Remaining: &v, ResetsAt: &reset, Capacity: 100}})
	}
	result := analyze(samples, analysisOptions{Now: now, Cadence: 5 * time.Minute, MaxGap: 45 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour})
	if len(result.Intervals) != 2 || result.RecentPpH == nil || *result.RecentPpH != 2 {
		t.Fatalf("30m cadence or future filter failed: %+v", result)
	}
}

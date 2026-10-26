package submon

import (
	"testing"
	"time"
)

func history(remaining ...float64) []sample {
	now := testTime()
	reset := now.Add(48 * time.Hour)
	out := []sample{}
	for i, n := range remaining {
		w := testWindow("weekly", n)
		w.ResetsAt = &reset
		out = append(out, sample{At: now.Add(time.Duration(i) * 5 * time.Minute), Window: w})
	}
	return out
}
func TestObservedRateAndCappedForecast(t *testing.T) {
	samples := history(90, 89, 88, 87, 86, 85, 84, 83, 82, 81, 80, 79, 78)
	now := samples[len(samples)-1].At
	a := analyze(samples, analysisOptions{Now: now, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour, Recurrence: true})
	if a.RecentPpH == nil || *a.RecentPpH < 11.99 || *a.RecentPpH > 12.01 {
		t.Fatalf("rate %+v", a)
	}
	if a.Peak == nil || a.Peak.PpH != 12 || a.Peak.Coverage != 1 {
		t.Fatalf("peak %+v", a.Peak)
	}
	if a.SustainablePpH == nil || a.ExhaustsAt == nil {
		t.Fatal("pace missing")
	}
	resetFound := false
	for _, p := range a.Forecast {
		if p.Remaining < 0 || p.Remaining > 100 {
			t.Fatal("uncapped", p)
		}
		if p.Assumption == "reported" && p.Remaining == 100 {
			resetFound = true
		}
	}
	if !resetFound {
		t.Fatal("reported reset did not restore cap")
	}
}
func TestResetAdjustmentGapAndFailureBreakDeltas(t *testing.T) {
	for _, kind := range []string{"reset", "adjustment", "gap", "failure", "capacity"} {
		t.Run(kind, func(t *testing.T) {
			s := history(80, 79, 78, 77)
			switch kind {
			case "reset":
				reset := s[1].At
				s[0].Window.ResetsAt = &reset
				s[1].Window.Remaining = number(100)
			case "adjustment":
				s[1].Window.Remaining = number(90)
			case "gap":
				s[1].At = s[0].At.Add(time.Hour)
			case "failure":
				s[1].Window.Remaining = nil
			case "capacity":
				s[1].Window.Capacity = 110
			}
			a := analyze(s[:2], analysisOptions{Now: s[1].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
			if a.RecentPpH != nil || len(a.Intervals) != 0 || a.Peak != nil || len(a.Forecast) != 0 {
				t.Fatalf("invalid interval used %+v", a)
			}
		})
	}
}
func TestCrossBoundaryConsumptionUnattributed(t *testing.T) {
	s := history(80, 70)
	s[0].At = testTime().Add(-2 * time.Minute)
	s[1].At = testTime().Add(3 * time.Minute)
	a := analyze(s, analysisOptions{Now: s[1].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour})
	if a.UnattributedPp != 10 || len(a.Unattributed) != 1 {
		t.Fatalf("cross-hour lost %+v", a)
	}
	for _, b := range a.Hourly {
		if b.ConsumedPp != 0 {
			t.Fatal("uncertain hourly attribution", b)
		}
	}
	if a.Coverage.Complete {
		t.Fatal("cross-hour marked complete")
	}
}
func TestInsufficientAndStaleHistory(t *testing.T) {
	s := history(80)
	a := analyze(s, analysisOptions{Now: s[0].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour})
	if a.Insufficient == "" || a.RecentPpH != nil {
		t.Fatal("invented history")
	}
	s = history(80, 79, 78)
	a = analyze(s, analysisOptions{Now: s[len(s)-1].At.Add(time.Hour), Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour})
	if len(a.Forecast) != 0 || a.Insufficient == "" {
		t.Fatal("stale forecast")
	}
}

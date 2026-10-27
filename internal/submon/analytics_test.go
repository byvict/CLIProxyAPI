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

func TestCrossDayAndResetUnusedBalance(t *testing.T) {
	midnight := time.Date(2027, 1, 12, 0, 0, 0, 0, saoPaulo)
	s := history(80, 70)
	s[0].At = midnight.Add(-2 * time.Minute)
	s[1].At = midnight.Add(3 * time.Minute)
	a := analyze(s, analysisOptions{Now: s[1].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour})
	for _, b := range a.Daily {
		if b.ConsumedPp != 0 {
			t.Fatal("cross-day delta attributed")
		}
	}
	if len(a.Unattributed) != 1 {
		t.Fatal("uncertain interval absent")
	}
	s = history(40, 100)
	reset := s[0].At.Add(time.Minute)
	next := reset.Add(7 * 24 * time.Hour)
	s[0].Window.ResetsAt = &reset
	s[1].Window.ResetsAt = &next
	a = analyze(s, analysisOptions{Now: s[1].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour})
	if len(a.UnusedCycles) != 1 || a.UnusedCycles[0].UpperBoundPp != 40 || len(a.Intervals) != 0 {
		t.Fatal("reset residual not bounded")
	}
}

func TestForecastWaitsForPostResetObservation(t *testing.T) {
	s := history(80, 79, 78)
	reset := s[len(s)-1].At.Add(time.Minute)
	for i := range s {
		s[i].Window.ResetsAt = &reset
	}
	a := analyze(s, analysisOptions{Now: reset.Add(time.Second), Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if len(a.Forecast) > 0 || a.Insufficient == "" {
		t.Fatal("projected from completed cycle")
	}
}

func TestForecastAndExhaustionUseObservationAge(t *testing.T) {
	s := history(80, 79, 78)
	now := s[2].At.Add(5 * time.Minute)
	a := analyze(s, analysisOptions{Now: now, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if len(a.Forecast) == 0 || a.Forecast[0].Remaining != 77 || a.ExhaustsAt == nil {
		t.Fatal("forecast ignored reading age")
	}
	expected := now.Add(time.Duration(77.0 / 12 * float64(time.Hour)))
	if !a.ExhaustsAt.Equal(expected) {
		t.Fatal("exhaustion disagrees with forecast")
	}
}

func TestMuseHourlyRateToleratesProbeAndPanelLatency(t *testing.T) {
	s := history(80, 79, 78)
	for i := range s {
		s[i].At = testTime().Add(time.Duration(i) * (30*time.Minute + time.Second))
	}
	now := s[2].At.Add(3 * time.Second)
	a := analyze(s, analysisOptions{Now: now, Cadence: 30 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 24 * time.Hour})
	if a.RecentPpH == nil || *a.RecentPpH < 1.99 || *a.RecentPpH > 2.01 || len(a.Forecast) == 0 {
		t.Fatal("normal mint latency prevented rate estimation")
	}
	if a.RateCoverage.Fraction < .99 || a.RateCoverage.Complete {
		t.Fatal("latency coverage hidden")
	}
}
func TestCompletedCyclesRespectSelectedPeriod(t *testing.T) {
	s := history(40, 100)
	reset := s[0].At.Add(time.Minute)
	next := reset.Add(7 * 24 * time.Hour)
	s[0].Window.ResetsAt = &reset
	s[1].Window.ResetsAt = &next
	a := analyze(s, analysisOptions{Now: s[1].At.Add(48 * time.Hour), Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour})
	if len(a.UnusedCycles) != 0 {
		t.Fatal("cycle outside selected period reported")
	}
}

func TestTinyObservedRateCannotProducePastExhaustion(t *testing.T) {
	s := history(80, 80-1e-12, 80-2e-12)
	a := analyze(s, analysisOptions{Now: s[2].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour})
	if a.RecentPpH == nil || a.ExhaustsAt != nil {
		t.Fatal("tiny rate overflowed exhaustion time")
	}
}

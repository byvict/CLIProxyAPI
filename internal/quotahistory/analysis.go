package quotahistory

import (
	"math"
	"sort"
	"time"
	_ "time/tzdata"
)

var saoPaulo = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic(err)
	}
	return l
}()

type analysisOptions struct {
	Now                                time.Time
	Cadence, Period, Lookback, Horizon time.Duration
	Recurrence                         bool
}
type deltaInterval struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	ConsumedPp float64   `json:"consumedPp"`
}
type chartPoint struct {
	At        time.Time `json:"at"`
	Remaining *float64  `json:"remaining"`
}
type ratePoint struct {
	BreakBefore bool      `json:"breakBefore"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	PpH         float64   `json:"ppH"`
	Coverage    float64   `json:"coverage"`
}
type consumptionBin struct {
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	ConsumedPp      float64   `json:"consumedPp"`
	HasUnattributed bool      `json:"hasUnattributed"`
	Coverage        float64   `json:"coverage"`
	Complete        bool      `json:"complete"`
}
type heatCell struct {
	Weekday    int     `json:"weekday"`
	Hour       int     `json:"hour"`
	ConsumedPp float64 `json:"consumedPp"`
	Coverage   float64 `json:"coverage"`
	Complete   bool    `json:"complete"`
}
type forecastPoint struct {
	At         time.Time `json:"at"`
	Remaining  float64   `json:"remaining"`
	Assumption string    `json:"assumption"`
}
type coverage struct {
	ObservedSeconds  float64 `json:"observedSeconds"`
	RequestedSeconds float64 `json:"requestedSeconds"`
	Fraction         float64 `json:"fraction"`
	Complete         bool    `json:"complete"`
}
type unusedCycle struct {
	ResetAt        time.Time `json:"resetAt"`
	LastObservedAt time.Time `json:"lastObservedAt"`
	UpperBoundPp   float64   `json:"upperBoundPp"`
}
type analysis struct {
	Observed       []chartPoint     `json:"observed"`
	Intervals      []deltaInterval  `json:"intervals"`
	Rate           []ratePoint      `json:"rate"`
	Hourly         []consumptionBin `json:"hourly"`
	Daily          []consumptionBin `json:"daily"`
	Heatmap        []heatCell       `json:"heatmap"`
	Forecast       []forecastPoint  `json:"forecast"`
	RecentPpH      *float64         `json:"recentPpH"`
	SustainablePpH *float64         `json:"sustainablePpH"`
	ExhaustsAt     *time.Time       `json:"exhaustsAt"`
	Peak           *ratePoint       `json:"peak"`
	UnattributedPp float64          `json:"unattributedPp"`
	Unattributed   []deltaInterval  `json:"unattributed"`
	UnusedCycles   []unusedCycle    `json:"unusedCycles"`
	Insufficient   string           `json:"insufficient"`
	Coverage       coverage         `json:"coverage"`
	RateCoverage   coverage         `json:"rateCoverage"`
}

func emptyAnalysis() analysis {
	return analysis{Observed: []chartPoint{}, Intervals: []deltaInterval{}, Rate: []ratePoint{}, Hourly: []consumptionBin{}, Daily: []consumptionBin{}, Heatmap: []heatCell{}, Forecast: []forecastPoint{}, Unattributed: []deltaInterval{}, UnusedCycles: []unusedCycle{}, Insufficient: "Histórico insuficiente. Aguarde pelo menos três leituras contínuas."}
}
func sameReset(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Sub(*b).Abs() <= time.Minute)
}
func continuous(a, b sample, maxGap time.Duration) bool {
	return a.Window.Key == b.Window.Key && a.Window.Capacity == b.Window.Capacity && a.Window.Remaining != nil && b.Window.Remaining != nil && b.At.After(a.At) && b.At.Sub(a.At) <= maxGap && *b.Window.Remaining <= *a.Window.Remaining && sameReset(a.Window.ResetsAt, b.Window.ResetsAt) && (a.Window.ResetsAt == nil || a.Window.ResetsAt.After(b.At))
}
func overlap(a, b, c, d time.Time) time.Duration {
	start := a
	if c.After(start) {
		start = c
	}
	end := b
	if d.Before(end) {
		end = d
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}
func analyze(input []sample, o analysisOptions) analysis {
	a := emptyAnalysis()
	a.Coverage.RequestedSeconds = o.Period.Seconds()
	a.RateCoverage.RequestedSeconds = o.Lookback.Seconds()
	if len(input) == 0 {
		return a
	}
	samples := make([]sample, 0, len(input))
	for _, s := range input {
		if !s.At.After(o.Now) {
			samples = append(samples, s)
		}
	}
	if len(samples) == 0 {
		return a
	}
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
	start := o.Now.Add(-o.Period)
	maxGap := 2 * o.Cadence
	for i, s := range samples {
		if s.At.After(o.Now) {
			continue
		}
		if !s.At.Before(start) {
			if i > 0 && s.At.Sub(samples[i-1].At) > maxGap {
				if gap := samples[i-1].At.Add(maxGap); !gap.Before(start) {
					a.Observed = append(a.Observed, chartPoint{At: gap})
				}
			}
			a.Observed = append(a.Observed, chartPoint{At: s.At, Remaining: s.Window.Remaining})
		}
		if i == 0 {
			continue
		}
		prev := samples[i-1]
		if !continuous(prev, s, maxGap) {
			if prev.Window.Remaining != nil && prev.Window.ResetsAt != nil && !prev.Window.ResetsAt.Before(start) && !prev.Window.ResetsAt.After(s.At) && !sameReset(prev.Window.ResetsAt, s.Window.ResetsAt) && s.Window.ResetsAt != nil && s.Window.ResetsAt.After(s.At) {
				a.UnusedCycles = append(a.UnusedCycles, unusedCycle{ResetAt: *prev.Window.ResetsAt, LastObservedAt: prev.At, UpperBoundPp: *prev.Window.Remaining})
			}
			continue
		}
		if prev.At.Before(start) {
			continue
		}
		a.Intervals = append(a.Intervals, deltaInterval{From: prev.At, To: s.At, ConsumedPp: *prev.Window.Remaining - *s.Window.Remaining})
	}
	for _, d := range a.Intervals {
		a.Coverage.ObservedSeconds += d.To.Sub(d.From).Seconds()
	}
	a.Coverage.Fraction = fraction(a.Coverage.ObservedSeconds, a.Coverage.RequestedSeconds)
	a.Hourly = bins(a.Intervals, start, o.Now, false)
	a.Daily = bins(a.Intervals, start, o.Now, true)
	for _, d := range a.Intervals {
		if !sameBin(d.From, d.To, false) {
			a.Unattributed = append(a.Unattributed, d)
			a.UnattributedPp += d.ConsumedPp
		}
	}
	a.Coverage.Complete = a.Coverage.Fraction >= 1-1e-9 && len(a.Unattributed) == 0
	a.Heatmap = heatmap(a.Hourly)
	a.Rate = rollingRates(a.Intervals)
	for i := range a.Rate {
		r := a.Rate[i]
		if r.Coverage >= 1-1e-9 && (a.Peak == nil || r.PpH > a.Peak.PpH) {
			a.Peak = &r
		}
	}
	cycleStart := samples[len(samples)-1].At
	for i := len(samples) - 1; i > 0; i-- {
		if !continuous(samples[i-1], samples[i], maxGap) {
			break
		}
		cycleStart = samples[i-1].At
	}
	lookbackStart := o.Now.Add(-o.Lookback)
	if cycleStart.After(lookbackStart) {
		lookbackStart = cycleStart
	}
	count := 0
	consumed := 0.0
	for _, d := range a.Intervals {
		duration := overlap(d.From, d.To, lookbackStart, o.Now)
		if duration <= 0 {
			continue
		}
		count++
		consumed += d.ConsumedPp * float64(duration) / float64(d.To.Sub(d.From))
		a.RateCoverage.ObservedSeconds += duration.Seconds()
	}
	a.RateCoverage.Fraction = fraction(a.RateCoverage.ObservedSeconds, a.RateCoverage.RequestedSeconds)
	a.RateCoverage.Complete = a.RateCoverage.Fraction >= 1-1e-9
	last := samples[len(samples)-1]
	w := last.Window
	if w.Remaining != nil && w.ResetsAt != nil && w.ResetsAt.After(o.Now) {
		v := *w.Remaining / w.ResetsAt.Sub(o.Now).Hours()
		a.SustainablePpH = &v
	}
	if count < 2 || a.RateCoverage.ObservedSeconds < o.Cadence.Seconds() {
		return a
	}
	rate := consumed / (a.RateCoverage.ObservedSeconds / 3600)
	a.RecentPpH = &rate
	if w.ResetsAt != nil && !w.ResetsAt.After(o.Now) {
		a.Insufficient = "O reset informado já passou. Aguarde uma leitura do novo ciclo."
		return a
	}
	if w.Remaining == nil || o.Now.Sub(last.At) > maxGap {
		a.Insufficient = "Leitura desatualizada ou ausente. Projeção suspensa."
		return a
	}
	if last.At.Sub(cycleStart) < 2*o.Cadence {
		a.RecentPpH = nil
		return a
	}
	a.Insufficient = ""
	projected := math.Max(0, *w.Remaining-rate*o.Now.Sub(last.At).Hours())
	if rate > 0 {
		hours := projected / rate
		maxHours := (14 * 24 * time.Hour).Hours()
		if o.Horizon > 0 {
			maxHours = o.Horizon.Hours()
		}
		beforeReset := w.ResetsAt == nil || hours < w.ResetsAt.Sub(o.Now).Hours()
		if hours <= maxHours && beforeReset {
			exhausts := o.Now.Add(time.Duration(hours * float64(time.Hour)))
			a.ExhaustsAt = &exhausts
		}
	}
	if o.Horizon > 0 {
		w.Remaining = &projected
		a.Forecast = forecast(w, o, rate)
	}
	return a
}
func fraction(n, d float64) float64 {
	if d <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, n/d))
}
func binStart(t time.Time, daily bool) time.Time {
	t = t.In(saoPaulo)
	if daily {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, saoPaulo)
	}
	return t.Truncate(time.Hour)
}
func binEnd(t time.Time, daily bool) time.Time {
	if daily {
		return t.AddDate(0, 0, 1)
	}
	return t.Add(time.Hour)
}
func sameBin(from, to time.Time, daily bool) bool {
	return binStart(from, daily).Equal(binStart(to.Add(-time.Nanosecond), daily))
}
func bins(intervals []deltaInterval, from, to time.Time, daily bool) []consumptionBin {
	out := []consumptionBin{}
	for start := binStart(from, daily); start.Before(to); start = binEnd(start, daily) {
		end := binEnd(start, daily)
		b := consumptionBin{From: start, To: end}
		seconds := 0.0
		for _, d := range intervals {
			n := overlap(start, end, d.From, d.To)
			if n == 0 {
				continue
			}
			seconds += n.Seconds()
			if !d.From.Before(start) && !d.To.After(end) {
				b.ConsumedPp += d.ConsumedPp
			} else {
				b.HasUnattributed = true
			}
		}
		b.Coverage = fraction(seconds, end.Sub(start).Seconds())
		b.Complete = b.Coverage >= 1-1e-9 && !b.HasUnattributed
		out = append(out, b)
	}
	return out
}
func heatmap(hourly []consumptionBin) []heatCell {
	out := make([]heatCell, 7*24)
	counts := make([]int, len(out))
	for i := range out {
		out[i] = heatCell{Weekday: i / 24, Hour: i % 24}
	}
	for _, b := range hourly {
		local := b.From.In(saoPaulo)
		i := int(local.Weekday())*24 + local.Hour()
		c := &out[i]
		c.ConsumedPp += b.ConsumedPp
		c.Coverage += b.Coverage
		counts[i]++
		if counts[i] == 1 {
			c.Complete = b.Complete
		} else {
			c.Complete = c.Complete && b.Complete
		}
	}
	for i := range out {
		if counts[i] > 0 {
			out[i].Coverage /= float64(counts[i])
		}
	}
	return out
}
func rollingRates(intervals []deltaInterval) []ratePoint {
	out := []ratePoint{}
	left := 0
	for i, last := range intervals {
		from := last.To.Add(-time.Hour)
		for left < i && !intervals[left].To.After(from) {
			left++
		}
		consumed, seconds := 0.0, 0.0
		for _, d := range intervals[left : i+1] {
			over := overlap(from, last.To, d.From, d.To)
			if over > 0 {
				seconds += over.Seconds()
				consumed += d.ConsumedPp * float64(over) / float64(d.To.Sub(d.From))
			}
		}
		if seconds > 0 {
			out = append(out, ratePoint{From: from, To: last.To, PpH: consumed / (seconds / 3600), Coverage: fraction(seconds, 3600), BreakBefore: len(out) > 0 && last.From.After(out[len(out)-1].To)})
		}
	}
	return out
}
func forecast(w windowReading, o analysisOptions, rate float64) []forecastPoint {
	end := o.Now.Add(o.Horizon)
	remaining := *w.Remaining
	last := o.Now
	out := []forecastPoint{{At: last, Remaining: remaining, Assumption: "estimate"}}
	resets := map[int64]string{}
	if w.ResetsAt != nil && w.ResetsAt.After(o.Now) {
		r := *w.ResetsAt
		if !r.After(end) {
			resets[r.UnixNano()] = "reported"
		}
		if o.Recurrence && w.Key.PeriodSeconds > 0 {
			for r = r.Add(time.Duration(w.Key.PeriodSeconds) * time.Second); !r.After(end); r = r.Add(time.Duration(w.Key.PeriodSeconds) * time.Second) {
				resets[r.UnixNano()] = "inferred"
			}
		}
	}
	times := []time.Time{end}
	for t := o.Now.Add(time.Hour); t.Before(end); t = t.Add(time.Hour) {
		times = append(times, t)
	}
	for ns := range resets {
		times = append(times, time.Unix(0, ns))
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for _, t := range times {
		if !t.After(last) {
			continue
		}
		remaining = math.Max(0, math.Min(w.Capacity, remaining-rate*t.Sub(last).Hours()))
		assumption := resets[t.UnixNano()]
		if assumption != "" {
			out = append(out, forecastPoint{At: t.Add(-time.Nanosecond), Remaining: remaining, Assumption: "estimate"})
			remaining = w.Capacity
		} else {
			assumption = "estimate"
		}
		out = append(out, forecastPoint{At: t, Remaining: remaining, Assumption: assumption})
		last = t
	}
	return out
}

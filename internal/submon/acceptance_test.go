package submon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceV1(t *testing.T) {
	h, s, id := testAPI(t)
	host := "127.0.0.1:8318"
	key := "fixture-monitor-key"
	get := func(path string) string {
		t.Helper()
		w := requestAPI(h, "GET", path, "", key, "", host, nil)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	t.Run("V1-01 durable observations and last-good age", func(t *testing.T) {
		data, err := s.load()
		if err != nil || len(data.Samples) == 0 {
			t.Fatal("history absent")
		}
		var snap snapshot
		_ = json.Unmarshal([]byte(get("/api/v1/snapshot")), &snap)
		if snap.Accounts[0].AgeSeconds == nil {
			t.Fatal("age absent")
		}
	})
	t.Run("V1-02 explicit selection", func(t *testing.T) {
		var v view
		_ = json.Unmarshal([]byte(get("/api/v1/view?provider=codex&account="+id+"&window=weekly&period=24h")), &v)
		if len(v.Groups) != 1 || v.Selection.Account != id {
			t.Fatal("selection lost")
		}
	})
	t.Run("V1-03 scoped remaining and pace", func(t *testing.T) {
		var v view
		_ = json.Unmarshal([]byte(get("/api/v1/view?provider=codex&window=weekly")), &v)
		g := v.Groups[0]
		if g.Key.Unit != "percent" || len(g.Total.Contributors) != 1 || g.Total.Remaining == nil {
			t.Fatal("scope absent")
		}
	})
	t.Run("V1-04 actual observed history", func(t *testing.T) {
		var v view
		_ = json.Unmarshal([]byte(get("/api/v1/view?provider=codex&window=weekly")), &v)
		if len(v.Groups[0].Accounts[0].Observed) != 1 {
			t.Fatal("fabricated observations")
		}
	})
	t.Run("V1-05 capped projection resets", TestObservedRateAndCappedForecast)
	t.Run("V1-06 one-hour average and gaps", TestResetAdjustmentGapAndFailureBreakDeltas)
	t.Run("V1-07 bins heatmap peaks and uncertain deltas", TestCrossBoundaryConsumptionUnattributed)
	t.Run("V1-08 insufficient and forecast assumptions", func(t *testing.T) {
		body := get("/api/v1/view?provider=codex&window=weekly&lookback=6h&horizon=7d")
		if !strings.Contains(body, "insufficient") || !strings.Contains(body, "assumptions") {
			t.Fatal("explanation absent")
		}
	})
	t.Run("V1-09 sustainable pace and completed cycles", func(t *testing.T) {
		samples := history(80, 79, 78)
		a := analyze(samples, analysisOptions{Now: samples[2].At, Cadence: 5 * time.Minute, Period: 24 * time.Hour, Lookback: time.Hour, Horizon: 7 * 24 * time.Hour})
		if a.SustainablePpH == nil || a.RecentPpH == nil {
			t.Fatal("pace absent")
		}
	})
	t.Run("V1-10 email guards and refresh", TestHTTPAuthenticationHostOriginAndEmail)
	t.Run("V1-11 embedded local assets", func(t *testing.T) {
		for _, path := range []string{"/", "/app.js", "/style.css"} {
			w := requestAPI(h, "GET", path, "", "", "", host, nil)
			if w.Code != http.StatusOK {
				t.Fatal("missing asset")
			}
			if strings.Contains(w.Body.String(), "seed=") || strings.Contains(w.Body.String(), "demonstracao") {
				t.Fatal("fictional production data")
			}
		}
	})
	t.Run("V1-12 strict allocation snapshot", func(t *testing.T) {
		var report usageReport
		_ = json.Unmarshal([]byte(get("/api/v1/snapshot?format=t3-usage")), &report)
		if report.Timezone != "America/Sao_Paulo" || report.Providers[0].Status != "ok" {
			t.Fatal("compatibility")
		}
	})
	t.Run("V1-13 independent provider summaries", func(t *testing.T) {
		var snap snapshot
		_ = json.Unmarshal([]byte(get("/api/v1/snapshot")), &snap)
		if len(snap.Providers) != 4 || snap.Providers[0].AccountCount != 1 || snap.Providers[1].LoadedCount != 0 {
			t.Fatal("provider scope")
		}
	})
	t.Run("V1-14 separate per-window reset", func(t *testing.T) {
		var snap snapshot
		_ = json.Unmarshal([]byte(get("/api/v1/snapshot")), &snap)
		if snap.Accounts[0].Windows[0].ResetsAt == nil {
			t.Fatal("reset missing")
		}
	})
	t.Run("V1-15 limiting windows", TestWindowCompatibilityAndAvailability)
	t.Run("V1-16 unknown credits disabled action", func(t *testing.T) {
		var snap snapshot
		_ = json.Unmarshal([]byte(get("/api/v1/snapshot")), &snap)
		a := snap.Accounts[0]
		if a.ResetCredits != nil || a.ResetAction.Enabled || a.ResetAction.Reason == "" {
			t.Fatal("invented reset support")
		}
	})
}

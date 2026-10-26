package submon

import (
	"testing"
	"time"
)

func TestIdentityStableAndProviderBound(t *testing.T) {
	a := identity{Provider: codex, Basis: providerAccount, Subject: "issuer|account|person"}
	if a.accountID() != a.accountID() {
		t.Fatal("identity changed")
	}
	b := a
	b.Subject = "issuer|account|other"
	if a.accountID() == b.accountID() {
		t.Fatal("account switch merged")
	}
	b = a
	b.Provider = grok
	if a.accountID() == b.accountID() {
		t.Fatal("providers merged")
	}
}

func TestWindowCompatibilityAndAvailability(t *testing.T) {
	weekly := testWindow("weekly", 100)
	session := testWindow("session", 100)
	session.Remaining = number(0)
	a := availabilityOf([]windowReading{weekly, session})
	if a.State != "unavailable" || len(a.Limiting) != 1 || a.Limiting[0].Kind != "session" {
		t.Fatalf("availability %+v", a)
	}
	weekly.Remaining = nil
	session.Remaining = nil
	if availabilityOf([]windowReading{weekly, session}).State != "unknown" {
		t.Fatal("missing became exhausted")
	}
	if weekly.Key == session.Key {
		t.Fatal("different windows compatible")
	}
	scoped := testWindow("weekly", 0)
	scoped.Key.Scope = "model-fable"
	a = availabilityOf([]windowReading{weekly, scoped})
	if a.State == "unavailable" || len(a.Limiting) != 1 {
		t.Fatal("model-specific limit treated as global", a)
	}
}

func testWindow(kind string, remaining float64) windowReading {
	period := int64(604800)
	if kind == "session" {
		period = 18000
	}
	return windowReading{Key: windowKey{Kind: kind, Scope: "all", PeriodSeconds: period, Unit: "percent", CapacityBasis: "fixture-pro"}, Label: kind, Remaining: number(remaining), Capacity: 100}
}
func number(n float64) *float64 { return &n }
func testTime() time.Time       { return time.Date(2027, 1, 11, 12, 0, 0, 0, time.UTC) }

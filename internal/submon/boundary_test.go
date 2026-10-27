package submon

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestModelAvailabilitySurvivesOptionalReturnTime(t *testing.T) {
	for _, value := range []string{`null`, `1800000000`, `1800000000.25`, `"1800000000"`, `"2027-01-15T00:00:00Z"`, `"not-a-time"`, `1e30`, `-1`} {
		t.Run(value, func(t *testing.T) {
			details := parseCodexDetails([]byte(fmt.Sprintf(`{"model_usage":{"fixture-model":{"available":false,"available_at":%s}}}`, value)))
			if len(details.Models) != 1 || details.Models[0].Available == nil || *details.Models[0].Available {
				t.Fatal("optional timestamp erased availability", details)
			}
			if strings.Contains(value, "1800000000") && (details.Models[0].AvailableAt == nil || details.Models[0].AvailableAt.Unix() != 1800000000) {
				t.Fatal("numeric return time lost")
			}
			if _, err := json.Marshal(details); err != nil {
				t.Fatal("unrepresentable timestamp escaped validation", err)
			}
		})
	}
}

func TestConflictingOptionalScopesDoNotEraseMainQuota(t *testing.T) {
	base := `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":604800}}`
	for _, tail := range []string{
		`,"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":20,"limit_window_seconds":18000}}},{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":30,"limit_window_seconds":18000}}}]}`,
		`,"additional_rate_limits":[{"limit_name":"all","rate_limit":{"primary_window":{"used_percent":20,"limit_window_seconds":604800}}}]}`,
		`,"additional_rate_limits":[{"limit_name":"spark","rate_limit":{"primary_window":{"used_percent":20}}}]}`,
		`,"code_review_rate_limit":{"allowed":false,"limit_reached":true,"primary_window":null,"secondary_window":null}}`,
	} {
		windows, err := parseWindows(codex, []byte(base+tail))
		if err != nil || availabilityOf(windows).State != "unknown" {
			t.Fatal("optional structure erased main quota or implied availability", err, windows)
		}
		main := 0
		for _, w := range windows {
			if w.Key.Scope == "all" {
				main++
				if w.Remaining == nil || *w.Remaining != 90 {
					t.Fatal("main quota changed")
				}
			} else if w.Remaining != nil {
				t.Fatal("conflicting scope retained a numeric claim")
			} else if !strings.Contains(w.Label, w.Key.Scope) {
				t.Fatal("unknown scope label is ambiguous")
			}
		}
		if main != 1 {
			t.Fatal("main quota missing")
		}
	}
	for _, nested := range []string{`null`, `{}`} {
		body := base + `,"additional_rate_limits":[{"limit_name":" spark ","rate_limit":` + nested + `,"primary_window":{"used_percent":100,"limit_window_seconds":18000}}]}`
		windows, err := parseWindows(codex, []byte(body))
		if err != nil || len(windows) != 2 || windows[1].Key.Scope != "spark" || *windows[1].Remaining != 0 {
			t.Fatal("empty nesting shadowed a flat scoped window", err, windows)
		}
	}
}

func TestIndexedPredecessorsPreserveLongGapCycleEvidence(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			s, err := openStore(testDir(t))
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			start := testTime()
			for i, at := range []time.Time{start.Add(-8 * time.Hour), start.Add(8 * time.Hour)} {
				if i == 1 && failure {
					p := probe{SourceID: "codex-native", StartedAt: start.Add(-time.Hour), At: start.Add(-time.Hour), Outcome: missing{Status: transportFailure}}
					if err = s.append(p, p.At, 1); err != nil {
						t.Fatal(err)
					}
				}
				p := successfulProbe(at, "person")
				r := p.Outcome.(reading)
				reset := start.Add(2*time.Hour + time.Duration(i)*7*24*time.Hour)
				r.Windows[0].ResetsAt = &reset
				r.Windows[0].Remaining = number(40 + float64(i)*60)
				p.Outcome = r
				if err = s.append(p, p.At, 0); err != nil {
					t.Fatal(err)
				}
			}
			full, err := s.load()
			if err != nil {
				t.Fatal(err)
			}
			bounded, err := s.loadSince(start)
			if err != nil {
				t.Fatal(err)
			}
			selection := viewSelection{Provider: "codex", Window: "weekly", Period: 24 * time.Hour, Lookback: time.Hour}
			a, err := makeView(full, nil, start.Add(24*time.Hour), selection)
			if err != nil {
				t.Fatal(err)
			}
			b, err := makeView(bounded, nil, start.Add(24*time.Hour), selection)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a.Groups[0].Accounts[0].UnusedCycles, b.Groups[0].Accounts[0].UnusedCycles) {
				t.Fatal("pre-period gap/failure changed cycle evidence")
			}
			if !failure && len(b.Groups[0].Accounts[0].UnusedCycles) != 1 {
				t.Fatal("long-gap reset missing")
			}
			if failure && len(b.Groups[0].Accounts[0].UnusedCycles) != 0 {
				t.Fatal("failure barrier lost")
			}
		})
	}
}

func TestFutureAttemptDoesNotDriveSourceStatus(t *testing.T) {
	s, err := openStore(testDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := successfulProbe(testTime(), "person")
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	p.At = p.At.Add(30 * time.Minute)
	p.StartedAt = p.At
	p.Outcome = missing{Status: authFailure}
	if err = s.append(p, p.At, 1); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	snap := makeSnapshot(data, nil, testTime().Add(5*time.Minute))
	if snap.Sources[0].Status != ok || snap.Accounts[0].Freshness != "fresh" {
		t.Fatal("future failure replaced current status")
	}
}

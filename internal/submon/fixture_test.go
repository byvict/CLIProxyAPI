package submon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func seedHistory(t *testing.T, s *store) time.Time {
	t.Helper()
	now := testTime()
	start := now.Add(-7 * 24 * time.Hour)
	specs := []struct {
		Source   string
		Provider provider
		Subject  string
		Offset   float64
	}{{"claude-native", claude, "fixture-claude-a", 0}, {"claude-fixture-secondary", claude, "fixture-claude-b", 20}, {"codex-native", codex, "fixture-codex", 35}, {"muse-native", muse, "fixture-muse", 45}}
	for _, spec := range specs {
		for i := 0; i <= 7*24*12; i++ {
			at := start.Add(time.Duration(i) * 5 * time.Minute)
			phase := int(spec.Offset+6) * 12
			remaining := 100 - float64((i+phase)%(7*24*12))*.02 - spec.Offset
			if remaining < 0 {
				remaining = 0
			}
			weekly := testWindow("weekly", remaining)
			weekly.Key.CapacityBasis = "fixture-plan"
			weekly.Label = "semanal"
			if spec.Provider == codex {
				weekly.Label = "7d"
			}
			nextReset := start.Add(time.Duration(((i+phase)/(7*24*12)+1)*(7*24*12)-phase) * 5 * time.Minute)
			weekly.ResetsAt = &nextReset
			session := testWindow("session", 100)
			session.Key.CapacityBasis = "fixture-plan"
			session.Label = "sessão"
			if spec.Provider == codex {
				session.Label = "5h"
			}
			sessionReset := at.Truncate(5 * time.Hour).Add(5 * time.Hour)
			session.ResetsAt = &sessionReset
			if spec.Subject == "fixture-claude-a" && i >= 7*24*12-12 {
				weekly.Remaining = number(0)
			}
			identity := identity{Provider: spec.Provider, Basis: providerAccount, Subject: spec.Subject, Email: spec.Subject + "@example.test"}
			if spec.Provider == muse {
				identity.Basis = providerEmail
			}
			windows := []windowReading{weekly, session}
			if spec.Provider == claude {
				scoped := weekly
				scoped.Key.Scope = "provider-label:Fable"
				scoped.Label = "semanal Fable"
				scoped.Remaining = number(94)
				if spec.Offset > 0 {
					scoped.Remaining = number(0)
				}
				windows = append(windows, scoped)
			}
			if spec.Provider == muse && i >= 7*24*12-12 {
				windows[1].Remaining = number(0)
			}
			p := probe{SourceID: spec.Source, StartedAt: at, At: at, Outcome: reading{Identity: identity, Windows: windows}}
			if spec.Provider == codex {
				value := p.Outcome.(reading)
				credits := 2
				value.Details.ResetCredits = &credits
				p.Outcome = value
			}
			if i > 300 && i < 310 {
				p.Outcome = missing{Status: transportFailure}
			}
			if err := s.append(p, at.Add(cadence(spec.Provider)), 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	return now
}
func TestUIFixtureServer(t *testing.T) {
	root := os.Getenv("SUBMON_FIXTURE_ROOT")
	if root == "" {
		t.Skip("run the verification script with --serve-fixtures")
	}
	if !filepath.IsAbs(root) || filepath.Base(root) != "fixture" {
		t.Fatal("fixture root must be a dedicated absolute fixture directory")
	}
	if _, err := os.Stat(filepath.Join(root, "history.sqlite")); err == nil {
		t.Fatal("fixture database already exists; use a new verification run directory")
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := seedHistory(t, s)
	key, err := loadOrCreateKey(filepath.Join(root, "access.key"))
	if err != nil {
		t.Fatal(err)
	}
	addr := os.Getenv("SUBMON_FIXTURE_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:18318"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("fixture listener must be loopback")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: newAPI(s, newCollector(s, nil, func() time.Time { return now }), key, func() time.Time { return now }, listener.Addr().String())}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(usageSnapshot(makeSnapshot(data, nil, now), now), "", "  ")
	if err = os.WriteFile(filepath.Join(root, "t3-usage.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "server.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Fixture URL http://%s\nPrivate monitor key file %s\nStop with SIGTERM to PID in %s\n", listener.Addr(), filepath.Join(root, "access.key"), filepath.Join(root, "server.pid"))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		if err = server.Close(); err != nil {
			t.Fatal(err)
		}
	case err = <-done:
		if err != http.ErrServerClosed {
			t.Fatal(err)
		}
	}
}

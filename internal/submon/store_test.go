package submon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func successfulProbe(at time.Time, subject string) probe {
	w := testWindow("weekly", 80)
	r := at.Add(24 * time.Hour)
	w.ResetsAt = &r
	return probe{SourceID: "codex-native", StartedAt: at, At: at, Outcome: reading{Identity: identity{Provider: codex, Basis: providerAccount, Subject: subject, Email: "fixture-private@example.test"}, Windows: []windowReading{w}}}
}
func TestStoreReopenIdentityBindingsAndIdempotence(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openStore(dir); err == nil {
		other.close()
		t.Fatal("second runtime owner accepted")
	}
	p := successfulProbe(testTime(), "person-a")
	if err = s.append(p, testTime().Add(5*time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	if err = s.append(p, testTime().Add(5*time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	p2 := successfulProbe(testTime().Add(5*time.Minute), "person-a")
	if err = s.append(p2, p2.At.Add(5*time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	p3 := successfulProbe(testTime().Add(10*time.Minute), "person-b")
	if err = s.append(p3, p3.At.Add(time.Hour), 2); err != nil {
		t.Fatal(err)
	}
	before, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Accounts) != 2 || len(before.Attempts) != 3 || len(before.Bindings) != 2 {
		t.Fatalf("unexpected persisted state %+v", before)
	}
	active := 0
	for _, b := range before.Bindings {
		if b.To == nil {
			active++
		}
	}
	if active != 1 {
		t.Fatal("binding switch failed")
	}
	if err = s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	after, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Accounts) != 2 || len(after.Attempts) != 3 || len(after.Samples) != 3 {
		t.Fatal("history lost")
	}
	state := after.Sources["codex-native"]
	if !state.NextAt.Equal(p3.At.Add(time.Hour)) || state.Failures != 2 {
		t.Fatal("backoff lost", state)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("fixture-minted-secret")) || bytes.Contains(b, []byte("fixture-secret")) {
			t.Fatal("secret persisted")
		}
	}
}
func TestUnverifiedIdentityNeverGetsReadings(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := successfulProbe(testTime(), "known")
	if err = s.append(p, p.At, 0); err != nil {
		t.Fatal(err)
	}
	p.At = p.At.Add(5 * time.Minute)
	p.StartedAt = p.At
	p.Outcome = missing{Status: identityUnverified}
	if err = s.append(p, p.At.Add(10*time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	data, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Samples) != 1 {
		t.Fatal("unverified quota attached")
	}
	for _, b := range data.Bindings {
		if b.To == nil {
			t.Fatal("unverified source still identifies current account")
		}
	}
}

package submon

import (
	"encoding/json"
	"os"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestProviderPayloads(t *testing.T) {
	cases := []struct {
		p     provider
		file  string
		count int
	}{{codex, "codex.json", 2}, {claude, "claude.json", 3}, {grok, "grok.json", 1}, {muse, "muse.json", 2}}
	for _, c := range cases {
		t.Run(string(c.p), func(t *testing.T) {
			windows, err := parseWindows(c.p, fixture(t, c.file))
			if err != nil {
				t.Fatal(err)
			}
			if len(windows) != c.count {
				t.Fatalf("windows %+v", windows)
			}
			for _, w := range windows {
				if w.Key.Unit != "percent" || w.Capacity != 100 {
					t.Fatalf("window %+v", w)
				}
			}
			if c.p == codex && windows[1].Remaining != nil {
				t.Fatal("null became zero")
			}
			if c.p == claude && (*windows[0].Remaining != 100 || *windows[1].Remaining != 0 || windows[2].Key.Scope != "fable") {
				t.Fatal("scope/zero lost")
			}
		})
	}
}
func TestMuseIdentityAndDormancy(t *testing.T) {
	r := parseMuse(fixture(t, "muse.json"))
	got, ok := r.(reading)
	if !ok || got.Identity.Basis != providerEmail || got.Identity.Subject != "fixture-muse@example.test" {
		t.Fatalf("identity %+v", r)
	}
	for _, name := range []string{"muse-dormant.json"} {
		if _, ok := parseMuse(fixture(t, name)).(missing); !ok {
			t.Fatal("dormancy fabricated")
		}
	}
	var raw map[string]any
	_ = json.Unmarshal(fixture(t, "muse.json"), &raw)
	delete(raw, "user_email")
	data, _ := json.Marshal(raw)
	m, ok := parseMuse(data).(missing)
	if !ok || m.Status != identityUnverified {
		t.Fatal("identity guessed")
	}
}
func TestNativeAccountIdentity(t *testing.T) {
	for _, p := range []provider{codex, grok} {
		file := string(p) + "-auth.json"
		c, err := parseCredential(p, fixture(t, file))
		if err != nil {
			t.Fatal(err)
		}
		if c.Identity.Subject == "" {
			t.Fatal("missing subject")
		}
		again, err := parseCredential(p, fixture(t, file))
		if err != nil || c.Identity.accountID() != again.Identity.accountID() {
			t.Fatal("unstable")
		}
	}
	if _, err := parseCredential(grok, []byte(`{"https://accounts.x.ai/sign-in":{"auth_mode":"api_key","key":"fixture-secret"}}`)); err == nil {
		t.Fatal("API key became subscription")
	}
}

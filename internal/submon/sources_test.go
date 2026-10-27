package submon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestClaudeProfileAndUsageUseOneCredentialSnapshot(t *testing.T) {
	calls := 0
	reads := 0
	n := nativeAccess{getenv: func(k string) string { return "" }, lookup: func(string) (string, bool) { return "", false }, platform: "darwin", home: "/fixture", keychain: func(context.Context, string, string) ([]byte, error) {
		reads++
		return []byte(`{"claudeAiOauth":{"accessToken":"fixture-access-secret"}}`), nil
	}}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, deadline := r.Context().Deadline(); deadline {
			t.Error("quota deadline")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-access-secret" {
			t.Error("credential mismatch")
		}
		calls++
		name := "claude.json"
		if strings.HasSuffix(r.URL.Path, "profile") {
			name = "claude-profile.json"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(fixture(t, name))), Header: http.Header{}}, nil
	})}
	p := (quotaReader{native: n, client: client}).probe(probeContext{Context: context.Background(), Now: testTime}, claude)
	if _, valid := p.Outcome.(reading); !valid || calls != 2 || reads != 1 {
		t.Fatalf("probe %T; calls %d reads %d", p.Outcome, calls, reads)
	}
}
func TestQuotaTransportAndSanitizedErrors(t *testing.T) {
	c := quotaClient()
	tr := c.Transport.(*http.Transport)
	if c.Timeout != 0 || tr.ExpectContinueTimeout != 0 || tr.ResponseHeaderTimeout != 0 || tr.TLSHandshakeTimeout != 0 || tr.IdleConnTimeout != 0 {
		t.Fatal("quota timeout")
	}
	r := quotaReader{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(`fixture-private-secret`))}, nil
	})}}
	_, m := r.request(context.Background(), testTime, "https://fixture.test/quota", "fixture-token", nil, false)
	if m == nil || m.Status != rateLimited || !m.RetryAt.Equal(testTime().Add(time.Hour)) {
		t.Fatal("backoff", m)
	}
	b, _ := json.Marshal(m)
	if bytes.Contains(b, []byte("fixture-private-secret")) {
		t.Fatal("raw error leaked")
	}
}
func TestSupportedNativeLocations(t *testing.T) {
	env := map[string]string{"CODEX_HOME": "/native/codex", "CLAUDE_CONFIG_DIR": "/native/claude", "MUSE_AUTH_PATH": "/native/muse.json"}
	paths := []string{}
	n := nativeAccess{home: "/native/home", getenv: func(k string) string { return env[k] }, lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, platform: "linux", readFile: func(path string) ([]byte, error) {
		paths = append(paths, path)
		if strings.Contains(path, "codex") {
			return fixture(t, "codex-auth.json"), nil
		}
		if strings.Contains(path, "claude") {
			return []byte(`{"claudeAiOauth":{"accessToken":"fixture"}}`), nil
		}
		return []byte(`{"providers":{"meta":{"mechanism":"oauth","access_token":"fixture"}}}`), nil
	}}
	for _, p := range []provider{codex, claude, muse} {
		if _, err := n.acquire(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	expected := []string{"/native/codex/auth.json", "/native/claude/.credentials.json", "/native/muse.json"}
	for i, path := range paths {
		if path != expected[i] {
			t.Fatal("wrong active location", paths)
		}
	}
	_, service := n.claudeLocation()
	if service == "Claude Code-credentials" {
		t.Fatal("profile service not isolated")
	}
	env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = ""
	_, service = n.claudeLocation()
	if service != "Claude Code-credentials" {
		t.Fatal("explicit default secure storage ignored")
	}
}

func TestClaudeModelScopeWithProviderDisplayName(t *testing.T) {
	windows, err := parseWindows(claude, []byte(`{"limits":[{"kind":"weekly_scoped","scope":{"model":{"id":null,"display_name":"Fable"}},"percent":33,"resets_at":null}]}`))
	if err != nil || len(windows) != 1 || windows[0].Key.Scope != "provider-label:Fable" {
		t.Fatalf("provider label scope rejected %v %+v", err, windows)
	}
}

func TestNativeTokenRotationPreservesAccountIdentity(t *testing.T) {
	for _, p := range []provider{codex, grok} {
		original := fixture(t, string(p)+"-auth.json")
		rotated := bytes.ReplaceAll(original, []byte("fixture-secret"), []byte("fixture-rotated-secret"))
		first, err := parseCredential(p, original)
		if err != nil {
			t.Fatal(err)
		}
		second, err := parseCredential(p, rotated)
		if err != nil {
			t.Fatal(err)
		}
		if first.Access == second.Access || first.Identity.accountID() != second.Identity.accountID() {
			t.Fatal("rotation split history")
		}
	}
	first := parseMuse(fixture(t, "muse.json")).(reading)
	rotated := parseMuse(bytes.ReplaceAll(fixture(t, "muse.json"), []byte("fixture-minted-secret"), []byte("fixture-rotated-secret"))).(reading)
	if first.Identity.accountID() != rotated.Identity.accountID() {
		t.Fatal("minted key rotation split Muse history")
	}
	switched := parseMuse(bytes.ReplaceAll(fixture(t, "muse.json"), []byte("Fixture-Muse@Example.test"), []byte("other@example.test"))).(reading)
	if first.Identity.accountID() == switched.Identity.accountID() {
		t.Fatal("Muse email change merged histories")
	}
}

func TestProviderWindowDurationsAndResetTimesMustBeRepresentable(t *testing.T) {
	for _, payload := range []string{`{"rate_limit":{"primary_window":{"limit_window_seconds":9223372036854775807,"used_percent":0}}}`, `{"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":0,"reset_at":253402300800}}}`} {
		if _, err := parseWindows(codex, []byte(payload)); err == nil {
			t.Fatal("unrepresentable provider time accepted")
		}
	}
}

func TestClaudeScopedGroupCannotLoseItsModel(t *testing.T) {
	if _, err := parseWindows(claude, []byte(`{"limits":[{"group":"weekly_scoped","percent":20,"scope":null}]}`)); err == nil {
		t.Fatal("model-specific group became an all-model window")
	}
}

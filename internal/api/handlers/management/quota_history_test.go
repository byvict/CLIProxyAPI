package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotahistory"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestQuotaHistoryUsesRegisteredCredentialAndValidatesBatch(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "history-account", FileName: "history-account.json", Provider: "claude"}
	index := auth.EnsureIndex()
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&config.Config{}, filepath.Join(t.TempDir(), "config.yaml"), manager)
	remaining := 65.0
	reset := time.Now().Add(time.Hour).UnixMilli()
	period := 168.0
	observation := quotahistory.Observation{Credential: "forged-account", Provider: "forged-provider", WindowID: "weekly", Label: "Weekly", ObservedAtMs: time.Now().UnixMilli(), Remaining: &remaining, ResetAtMs: &reset, PeriodHours: &period}
	post := func(authIndex string, observations []quotahistory.Observation) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]any{"authIndex": authIndex, "observations": observations})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/quota-history", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.RecordQuotaHistory(c)
		c.Writer.WriteHeaderNow()
		return rec
	}
	if rec := post(index, []quotahistory.Observation{observation}); rec.Code != http.StatusNoContent {
		t.Fatalf("record: %d %s", rec.Code, rec.Body.String())
	}
	stored, err := quotahistory.Read(context.Background(), h.quotaHistoryPath(), 0, time.Now().UnixMilli())
	if err != nil || len(stored) != 1 || stored[0].Credential != h.quotaHistoryCredential(auth) || stored[0].Provider != "claude" {
		t.Fatalf("identity not resolved from credential: %+v, %v", stored, err)
	}
	invalid := observation
	invalid.ObservedAtMs++
	negative := -1.0
	invalid.Remaining = &negative
	if rec := post(index, []quotahistory.Observation{observation, invalid}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid batch accepted: %d", rec.Code)
	}
	if rec := post("unknown", []quotahistory.Observation{observation}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown credential accepted: %d", rec.Code)
	}
	for _, query := range []string{"period=7d", "period=invalid"} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v8/management/quota-history?"+query, nil)
		h.GetQuotaHistory(c)
		if query == "period=invalid" && rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid period accepted: %d", rec.Code)
		}
		if query == "period=7d" && (rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("read failed: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestQuotaHistoryPollingUsesThirtyMinutesForEveryProvider(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "xai", "meta"} {
		t.Run(provider, func(t *testing.T) {
			manager := coreauth.NewManager(nil, nil, nil)
			auth := &coreauth.Auth{ID: "poll-account", FileName: "poll-account.json", Provider: provider}
			index := auth.EnsureIndex()
			if _, err := manager.Register(context.Background(), auth); err != nil {
				t.Fatal(err)
			}
			h := NewHandler(&config.Config{}, filepath.Join(t.TempDir(), "config.yaml"), manager)
			body, err := json.Marshal(map[string]string{"authIndex": index})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/quota-history/poll", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			before := time.Now()
			h.ReserveQuotaHistoryPoll(c)
			if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"claimed":true`)) {
				t.Fatalf("first poll: %d %s", rec.Code, rec.Body.String())
			}
			for _, step := range []struct {
				minutes int
				claimed bool
			}{{29, false}, {31, true}} {
				claimed, err := quotahistory.ReservePoll(context.Background(), h.quotaHistoryPath(), h.quotaHistoryCredential(auth),
					before.Add(time.Duration(step.minutes)*time.Minute).UnixMilli(), (30 * time.Minute).Milliseconds(), false)
				if err != nil || claimed != step.claimed {
					t.Fatalf("poll after %d minutes: claimed %v, want %v, error %v", step.minutes, claimed, step.claimed, err)
				}
			}
		})
	}
}

func TestQuotaHistoryAllIncludesOlderAccountsWithoutMakingThemCurrent(t *testing.T) {
	h := NewHandler(&config.Config{}, filepath.Join(t.TempDir(), "config.yaml"), nil)
	remaining := 33.0
	period := 168.0
	old := time.Now().Add(-70 * 24 * time.Hour).UnixMilli()
	if err := quotahistory.Record(context.Background(), h.quotaHistoryPath(), []quotahistory.Observation{{
		Credential: "codexbar:old", Provider: "codex", WindowID: "weekly", Label: "CodexBar weekly",
		ObservedAtMs: old, Remaining: &remaining, PeriodHours: &period,
	}}); err != nil {
		t.Fatal(err)
	}
	windowID := ""
	for _, endpoint := range []string{"raw", "snapshot", "view"} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/?period=all&provider=codex&account=codexbar:old&window="+url.QueryEscape(windowID), nil)
		switch endpoint {
		case "raw":
			h.GetQuotaHistory(c)
		case "snapshot":
			h.GetQuotaHistorySnapshot(c)
		case "view":
			h.GetQuotaHistoryView(c)
		}
		if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("codexbar:old")) {
			t.Fatalf("old history missing from %s: %d %s", endpoint, rec.Code, rec.Body.String())
		}
		if endpoint == "snapshot" && !bytes.Contains(rec.Body.Bytes(), []byte(`"active":false`)) {
			t.Fatal("historical account treated as current")
		}
		if endpoint == "snapshot" {
			var snapshot struct {
				Accounts []struct {
					HistoryWindows []struct {
						ID string `json:"id"`
					} `json:"historyWindows"`
				} `json:"accounts"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			windowID = snapshot.Accounts[0].HistoryWindows[0].ID
		}
	}
}

func TestQuotaHistorySurvivesInstallationMove(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	old := &coreauth.Auth{ID: "nested/account.json", FileName: "account.json", Provider: "codex", Attributes: map[string]string{"path": filepath.Join(root, "old/auths/nested/account.json")}}
	current := old.Clone()
	current.Attributes["path"] = filepath.Join(root, "new/auths/nested/account.json")
	current.Index = ""
	oldIndex, currentIndex := old.EnsureIndex(), current.EnsureIndex()
	if oldIndex == currentIndex {
		t.Fatal("test requires different runtime indices")
	}
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(ctx, old); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&config.Config{}, filepath.Join(root, "config.yaml"), manager)
	now := time.Now().UnixMilli()
	remaining := 60.0
	observation := quotahistory.Observation{Credential: oldIndex, Provider: "codex", WindowID: "weekly", Label: "Weekly", ObservedAtMs: now, Remaining: &remaining}
	if err := quotahistory.Record(ctx, h.quotaHistoryPath(), []quotahistory.Observation{observation}); err != nil {
		t.Fatal(err)
	}
	read := func() []quotahistory.Observation {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/?period=all", nil)
		h.GetQuotaHistory(c)
		if rec.Code != 200 {
			t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Observations []quotahistory.Observation `json:"observations"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Observations
	}
	if got := read(); len(got) != 1 || got[0].Credential != oldIndex {
		t.Fatalf("before move: %+v", got)
	}
	manager = coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(ctx, current); err != nil {
		t.Fatal(err)
	}
	h.authManager = manager
	for i := 0; i < 2; i++ {
		got := read()
		if len(got) != 1 || got[0].Credential != currentIndex || got[0].ObservedAtMs != now || *got[0].Remaining != 60 {
			t.Fatalf("after move: %+v", got)
		}
	}
	other := current.Clone()
	other.ID = "different/account.json"
	if h.quotaHistoryCredential(other) == h.quotaHistoryCredential(current) {
		t.Fatal("different relative paths share history")
	}
}

package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if err != nil || len(stored) != 1 || stored[0].Credential != index || stored[0].Provider != "claude" {
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

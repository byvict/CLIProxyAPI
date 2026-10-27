package management

import (
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotahistory"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) quotaHistoryPath() string {
	return filepath.Join(filepath.Dir(h.configFilePath), "quota-history.sqlite")
}

func (h *Handler) GetQuotaHistory(c *gin.Context) {
	periods := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
	period, ok := periods[c.DefaultQuery("period", "30d")]
	all := c.Query("period") == "all"
	if !ok && !all {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quota history period"})
		return
	}
	now := time.Now()
	since := int64(0)
	if !all {
		since = now.Add(-period).UnixMilli()
	}
	observations, err := quotahistory.Read(c.Request.Context(), h.quotaHistoryPath(), since, now.UnixMilli())
	if err != nil {
		log.WithError(err).Error("read quota history")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read quota history"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"observations": observations})
}

func (h *Handler) quotaHistoryViewData(c *gin.Context) ([]quotahistory.Observation, quotahistory.Snapshot, time.Time, bool) {
	now := time.Now()
	observations, err := quotahistory.Read(c.Request.Context(), h.quotaHistoryPath(), 0, now.UnixMilli())
	if err != nil {
		log.WithError(err).Error("read quota history view")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read quota history"})
		return nil, quotahistory.Snapshot{}, now, false
	}
	credentials := []quotahistory.Credential{}
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			label := auth.FileName
			if label == "" {
				label = auth.ID
			}
			credentials = append(credentials, quotahistory.Credential{ID: auth.EnsureIndex(), Provider: auth.Provider, Label: label, Active: !auth.Disabled})
		}
	}
	c.Header("Cache-Control", "no-store")
	return observations, quotahistory.MakeSnapshot(observations, credentials, now), now, true
}

func (h *Handler) GetQuotaHistorySnapshot(c *gin.Context) {
	_, snapshot, _, ok := h.quotaHistoryViewData(c)
	if ok {
		c.JSON(http.StatusOK, snapshot)
	}
}

func (h *Handler) GetQuotaHistoryView(c *gin.Context) {
	periods := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
	period, valid := periods[c.DefaultQuery("period", "24h")]
	all := c.Query("period") == "all"
	if !valid && !all {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quota history period"})
		return
	}
	observations, snapshot, now, ok := h.quotaHistoryViewData(c)
	if ok {
		if all {
			period = 24 * time.Hour
			for _, observation := range observations {
				period = max(period, now.Sub(time.UnixMilli(observation.ObservedAtMs)))
			}
		}
		c.JSON(http.StatusOK, quotahistory.MakeView(observations, snapshot, c.Query("provider"), c.Query("account"), c.Query("window"), period, now))
	}
}

func (h *Handler) RecordQuotaHistory(c *gin.Context) {
	var body struct {
		AuthIndex    string                     `json:"authIndex"`
		Observations []quotahistory.Observation `json:"observations"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Observations) == 0 || len(body.Observations) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quota history observations"})
		return
	}
	auth := h.authByIndex(strings.TrimSpace(body.AuthIndex))
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	now := time.Now().UnixMilli()
	for i := range body.Observations {
		observation := &body.Observations[i]
		if strings.TrimSpace(observation.WindowID) == "" || len(observation.WindowID) > 256 || len(observation.Label) > 256 ||
			observation.ObservedAtMs > now+60_000 || observation.ObservedAtMs < now-15*60_000 ||
			(observation.Remaining != nil && (math.IsNaN(*observation.Remaining) || math.IsInf(*observation.Remaining, 0) || *observation.Remaining < 0 || *observation.Remaining > 100)) ||
			(observation.PeriodHours != nil && (math.IsNaN(*observation.PeriodHours) || math.IsInf(*observation.PeriodHours, 0) || *observation.PeriodHours <= 0)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quota history observation"})
			return
		}
		// Identity comes from the authenticated proxy credential, never from browser labels.
		observation.Credential = auth.Index
		observation.Provider = auth.Provider
	}
	if err := quotahistory.Record(c.Request.Context(), h.quotaHistoryPath(), body.Observations); err != nil {
		log.WithError(err).Error("record quota history")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not record quota history"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ReserveQuotaHistoryPoll(c *gin.Context) {
	var body struct {
		AuthIndex string `json:"authIndex"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quota polling request"})
		return
	}
	auth := h.authByIndex(strings.TrimSpace(body.AuthIndex))
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	interval := 5 * time.Minute
	if auth.Provider == "meta" {
		// Muse's quota reader can mint an API key, so retain the monitor's 30-minute floor.
		interval = 30 * time.Minute
	}
	claimed, err := quotahistory.ReservePoll(c.Request.Context(), h.quotaHistoryPath(), auth.Index, time.Now().UnixMilli(), interval.Milliseconds())
	if err != nil {
		log.WithError(err).Error("reserve quota history poll")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not reserve quota poll"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"claimed": claimed})
}

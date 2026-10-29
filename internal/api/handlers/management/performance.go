package management

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/githubauth"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/performance"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) GetPerformance(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.activity == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "T3code data directory is unavailable"})
		return
	}
	report, err := h.activity.Report(c.Request.Context())
	if os.IsNotExist(err) {
		c.JSON(http.StatusOK, gin.H{"status": "unavailable"})
		return
	}
	if err != nil {
		log.WithError(err).Warn("read performance activity")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not read T3code activity"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "activity": report})
}

func (h *Handler) githubActivity(c *gin.Context, token string) (performance.GitHubActivity, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	h.mu.Lock()
	proxyURL := h.cfg.ProxyURL
	h.mu.Unlock()
	util.SetProxy(&sdkconfig.SDKConfig{ProxyURL: proxyURL}, client)
	zone, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return performance.GitHubActivity{}, err
	}
	return performance.ReadGitHub(c.Request.Context(), client, token, time.Now().In(zone))
}

func (h *Handler) GetGitHubInfra(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	token := githubauth.ResolveToken()
	source := "environment"
	h.mu.Lock()
	configuredToken := strings.TrimSpace(h.cfg.GitHubToken)
	h.mu.Unlock()
	if configuredToken != "" {
		token = configuredToken
		source = "configuration"
	}
	if token == "" {
		c.JSON(http.StatusOK, gin.H{"status": "disconnected"})
		return
	}
	activity, err := h.githubActivity(c, token)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "connected", "source": source, "activity": activity})
}

func (h *Handler) ConnectGitHubInfra(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var body struct {
		Token string `json:"token"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Token) == "" || strings.ContainsAny(body.Token, "\r\n") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A GitHub personal access token is required"})
		return
	}
	token := strings.TrimSpace(body.Token)
	if _, err := h.githubActivity(c, token); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := h.cfg.GitHubToken
	h.cfg.GitHubToken = token
	if !h.persistLocked(c) {
		h.cfg.GitHubToken = previous
		return
	}
	githubauth.SetToken(token)
}

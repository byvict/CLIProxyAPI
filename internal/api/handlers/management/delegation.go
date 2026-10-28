package management

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/delegation"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) GetDelegation(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	report, err := delegation.Read(filepath.Join(filepath.Dir(h.configFilePath), "delegation-observatory"))
	if os.IsNotExist(err) {
		c.JSON(http.StatusOK, gin.H{"status": "unavailable"})
		return
	}
	if err != nil {
		log.WithError(err).Warn("read delegation publication")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not read the validated delegation report"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "report": report})
}

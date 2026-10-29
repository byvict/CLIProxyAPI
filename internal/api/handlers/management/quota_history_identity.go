package management

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotahistory"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) quotaHistoryCredential(auth *coreauth.Auth) string {
	if auth.FileName == "" {
		return auth.EnsureIndex()
	}
	relative := auth.ID
	if filepath.IsAbs(relative) {
		var err error
		relative, err = filepath.Rel(h.configSnapshot().AuthDir, relative)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return auth.EnsureIndex()
		}
	}
	return quotahistory.CredentialID(auth.Provider, filepath.ToSlash(relative))
}

func (h *Handler) prepareQuotaHistory(c *gin.Context) (map[string]string, bool) {
	visible := map[string]string{}
	aliases := map[string]string{}
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			index := auth.EnsureIndex()
			stable := h.quotaHistoryCredential(auth)
			visible[stable] = index
			aliases[index] = stable
		}
	}
	if err := quotahistory.Rekey(c.Request.Context(), h.quotaHistoryPath(), aliases); err != nil {
		log.WithError(err).Error("resolve quota history identities")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not resolve quota history identities"})
		return nil, false
	}
	return visible, true
}

func quotaHistoryVisible(observations []quotahistory.Observation, identities map[string]string) {
	for i := range observations {
		if index, ok := identities[observations[i].Credential]; ok {
			observations[i].Credential = index
		}
	}
}

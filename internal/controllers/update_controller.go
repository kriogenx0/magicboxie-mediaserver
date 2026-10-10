package controllers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

// The server updates itself from main on the Pi host, outside its own
// container (scripts/pi-self-update.sh, run by magicboxie-self-update.timer).
// The two sides talk through files in the data dir, which the host sees at
// /var/lib/magicboxie-docker: the host writes update-status.json, and
// "Update now" drops update-request, which magicboxie-self-update.path
// watches.
const (
	updateStatusFile  = "update-status.json"
	updateRequestFile = "update-request"
)

// UpdateStatus is what the host updater last wrote. State is one of
// checking, installing, current, offline or failed; it's empty when no
// updater has run (dev, or a Pi not yet set up for self-update).
type UpdateStatus struct {
	State     string `json:"state"`
	Installed string `json:"installed,omitempty"`
	Latest    string `json:"latest,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
	Message   string `json:"message,omitempty"`
	Requested bool   `json:"requested"`
}

// UpdateStatus serves GET /api/update.
func (ic *InfoController) UpdateStatus(c *gin.Context) {
	var status UpdateStatus
	if data, err := os.ReadFile(filepath.Join(ic.dataDir, updateStatusFile)); err == nil {
		_ = json.Unmarshal(data, &status)
	}
	_, err := os.Stat(filepath.Join(ic.dataDir, updateRequestFile))
	status.Requested = err == nil
	c.JSON(http.StatusOK, status)
}

// RequestUpdate serves POST /api/update: asks the host to check main now
// instead of waiting for its daily timer. If main has anything new, the host
// rebuilds and restarts this container.
func (ic *InfoController) RequestUpdate(c *gin.Context) {
	path := filepath.Join(ic.dataDir, updateRequestFile)
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not request an update: " + err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"requested": true})
}

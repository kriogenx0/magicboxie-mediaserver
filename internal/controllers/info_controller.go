package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"magicboxie/internal/models"
	"magicboxie/internal/services/sysinfo"
)

// InfoController serves the admin Info page: the media server Pi's own
// health plus a few library totals.
type InfoController struct {
	db   *gorm.DB
	dirs []sysinfo.Dir
}

func NewInfoController(db *gorm.DB, moviesDir, musicDir, dataDir string) *InfoController {
	return &InfoController{db: db, dirs: []sysinfo.Dir{
		{Label: "Movie storage", Path: moviesDir},
		{Label: "Music storage", Path: musicDir},
		{Label: "App data", Path: dataDir},
		{Label: "System storage", Path: "/"},
	}}
}

func (ic *InfoController) Info(c *gin.Context) {
	var library struct {
		Movies     int64 `json:"movies"`
		Tracks     int64 `json:"tracks"`
		Devices    int64 `json:"devices"`
		JobsActive int64 `json:"jobs_active"`
	}
	ic.db.Model(&models.Movie{}).Count(&library.Movies)
	ic.db.Model(&models.Track{}).Count(&library.Tracks)
	ic.db.Model(&models.Device{}).Count(&library.Devices)
	ic.db.Model(&models.Job{}).Where("status IN ?", []string{models.JobStatusQueued, models.JobStatusRunning}).Count(&library.JobsActive)

	c.JSON(http.StatusOK, gin.H{
		"system":  sysinfo.Collect(c.Request.Context(), ic.dirs),
		"library": library,
	})
}

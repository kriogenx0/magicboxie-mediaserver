package controllers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"magicboxie/internal/models"
	"magicboxie/internal/services/transcode"
)

type VideosController struct {
	db        *gorm.DB
	moviesDir string
	dataDir   string
}

func NewVideosController(db *gorm.DB, moviesDir, dataDir string) *VideosController {
	return &VideosController{db: db, moviesDir: moviesDir, dataDir: dataDir}
}

// Preview lazily creates and caches a short, muted, browser-compatible clip.
func (vc *VideosController) Preview(c *gin.Context) {
	id, ok := parseItemID(c.Param("itemId"), "movie")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var movie models.Movie
	if err := vc.db.First(&movie, id).Error; err != nil || movie.Status != models.MovieStatusReady {
		c.JSON(http.StatusNotFound, gin.H{"error": "preview unavailable"})
		return
	}
	dir := filepath.Join(vc.dataDir, "previews")
	path := filepath.Join(dir, fmt.Sprintf("%d.mp4", id))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare preview"})
			return
		}
		tmp := path + ".tmp.mp4"
		start := movie.DurationSeconds * 0.2
		if start < 0 {
			start = 0
		}
		cmd := exec.CommandContext(c.Request.Context(), "ffmpeg", "-y", "-v", "error",
			"-ss", fmt.Sprintf("%.2f", start), "-i", filepath.Join(vc.moviesDir, movie.PlayableRelpath),
			"-t", "15", "-an", "-vf", "scale=960:-2", "-c:v", "libx264", "-preset", "veryfast",
			"-crf", "27", "-movflags", "+faststart", tmp)
		if output, err := cmd.CombinedOutput(); err != nil {
			os.Remove(tmp)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate preview", "detail": string(output)})
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save preview"})
			return
		}
	}
	ServeFileRange(c, path, "preview.mp4")
}

// Stream serves a movie's playable file with Range support at Jellyfin's
// conventional /Videos/{itemId}/stream path. Used by the web <video> tag,
// AVPlayer (magicboxie-appletv), and any client downloading the file directly
// (magicboxie-device's home-sync, magicboxie-ios).
func (vc *VideosController) Stream(c *gin.Context) {
	id, ok := parseItemID(c.Param("itemId"), "movie")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var movie models.Movie
	if err := vc.db.First(&movie, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return
	}
	if movie.PlayableRelpath == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "movie is not ready to stream yet"})
		return
	}

	fullPath := filepath.Join(vc.moviesDir, movie.PlayableRelpath)
	// Use the currently playable file's own extension for Content-Type
	// detection -- OriginalFilename is a historical record and may have a
	// stale extension (e.g. .mkv) once a transcode has replaced the file.
	ServeFileRange(c, fullPath, filepath.Base(movie.PlayableRelpath))
}

// Player serves a movie's 480p copy made for magicboxie-player (a
// MagicBoxie extension; see Movie.PlayerStatus), with Range support like
// Stream. 409 until the copy is ready.
func (vc *VideosController) Player(c *gin.Context) {
	id, ok := parseItemID(c.Param("itemId"), "movie")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var movie models.Movie
	if err := vc.db.First(&movie, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return
	}
	if movie.PlayerStatus != models.PlayerStatusReady {
		c.JSON(http.StatusConflict, gin.H{"error": "player copy is not ready yet"})
		return
	}
	ServeFileRange(c, transcode.PlayerCopyPath(vc.dataDir, movie.ID), "player.mp4")
}

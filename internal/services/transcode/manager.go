// Package transcode runs background ffmpeg transcode jobs for movies that
// aren't already browser/iOS compatible, plus the small 480p copies
// magicboxie-player downloads (see PlayerFFmpegArgs): a small in-process worker pool
// (no external broker needed for a single-box personal server), with DB-
// backed job state so progress survives a server restart.
package transcode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"magicboxie/internal/models"
	"magicboxie/internal/services/events"
)

const progressUpdateInterval = 1 * time.Second

// PlayerFFmpegArgs is the video/audio encoding for magicboxie-player's copy:
// 480p at most (never upscaled), H.264 Baseline - no CABAC or B-frames, the
// most CPU-expensive parts of decode - which the player's Pi Zero can play
// smoothly. Matches what the player would otherwise encode for itself.
var PlayerFFmpegArgs = []string{
	"-vf", "scale=-2:'min(480,ih)'",
	"-c:v", "libx264",
	"-profile:v", "baseline",
	"-level", "3.1",
	"-pix_fmt", "yuv420p", // Baseline is 8-bit 4:2:0 only (10-bit/4:4:4 sources)
	"-c:a", "aac",
	"-b:a", "128k",
	"-ac", "2",
}

type Manager struct {
	db            *gorm.DB
	moviesDir     string
	dataDir       string
	preset        string
	crf           int
	maxConcurrent int
	hub           *events.Hub

	queue chan queuedJob // jobs awaiting a worker
}

type queuedJob struct {
	movieID uint
	jobType string
}

// PlayerCopyPath is where a movie's 480p copy for magicboxie-player lives.
func PlayerCopyPath(dataDir string, movieID uint) string {
	return filepath.Join(dataDir, "player", fmt.Sprintf("%d.mp4", movieID))
}

func NewManager(db *gorm.DB, moviesDir, dataDir, preset string, crf, maxConcurrent int, hub *events.Hub) *Manager {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Manager{
		db:            db,
		moviesDir:     moviesDir,
		dataDir:       dataDir,
		preset:        preset,
		crf:           crf,
		maxConcurrent: maxConcurrent,
		hub:           hub,
		queue:         make(chan queuedJob, 256),
	}
}

// Start launches the worker pool and recovers any jobs left queued/running
// by a previous process (a "running" job means the server crashed mid
// encode; it's reset to queued and re-run from scratch).
func (m *Manager) Start(ctx context.Context) {
	var staleJobs []models.Job
	m.db.Where("status IN ?", []string{models.JobStatusQueued, models.JobStatusRunning}).Find(&staleJobs)
	for _, job := range staleJobs {
		if job.Status == models.JobStatusRunning {
			m.db.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]interface{}{
				"status":           models.JobStatusQueued,
				"progress_percent": 0,
			})
		}
	}

	// Movies that ended up needs_transcode without an active job (e.g.
	// imported before this manager was wired up) get swept in too.
	var orphaned []models.Movie
	m.db.Where("status = ?", models.MovieStatusNeedsTranscode).Find(&orphaned)
	for _, movie := range orphaned {
		var count int64
		m.db.Model(&models.Job{}).
			Where("movie_id = ? AND type = ? AND status IN ?", movie.ID, models.JobTypeTranscode, []string{models.JobStatusQueued, models.JobStatusRunning}).
			Count(&count)
		if count == 0 {
			m.Enqueue(movie.ID)
		}
	}

	for i := 0; i < m.maxConcurrent; i++ {
		go m.worker(ctx)
	}

	for _, job := range staleJobs {
		m.queue <- queuedJob{movieID: job.MovieID, jobType: job.Type}
	}

	// Device-synced movies still missing their player copy (synced before
	// this existed, or its job was lost) get one now.
	var needPlayerCopy []models.Movie
	m.db.Where("status = ? AND sync_enabled = ? AND player_status IN ?",
		models.MovieStatusReady, true, []string{"", models.PlayerStatusPending}).Find(&needPlayerCopy)
	for _, movie := range needPlayerCopy {
		m.EnqueuePlayer(movie.ID)
	}
}

// Enqueue creates a queued transcode job for movieID and schedules it.
func (m *Manager) Enqueue(movieID uint) {
	job := &models.Job{MovieID: movieID, Type: models.JobTypeTranscode, Status: models.JobStatusQueued}
	if err := m.db.Create(job).Error; err != nil {
		log.Printf("transcode: failed to create job for movie %d: %v", movieID, err)
		return
	}
	m.queue <- queuedJob{movieID: movieID, jobType: models.JobTypeTranscode}
}

// EnqueuePlayer queues the 480p copy magicboxie-player downloads, for a
// ready, device-synced movie that doesn't have one yet. A no-op otherwise,
// so it's safe to call whenever a movie might have become eligible.
func (m *Manager) EnqueuePlayer(movieID uint) {
	var movie models.Movie
	if err := m.db.First(&movie, movieID).Error; err != nil {
		return
	}
	if movie.Status != models.MovieStatusReady || !movie.SyncEnabled || movie.PlayerStatus == models.PlayerStatusReady {
		return
	}
	var active int64
	m.db.Model(&models.Job{}).
		Where("movie_id = ? AND type = ? AND status IN ?", movieID, models.JobTypePlayerTranscode, []string{models.JobStatusQueued, models.JobStatusRunning}).
		Count(&active)
	if active > 0 {
		return
	}
	job := &models.Job{MovieID: movieID, Type: models.JobTypePlayerTranscode, Status: models.JobStatusQueued}
	if err := m.db.Create(job).Error; err != nil {
		log.Printf("transcode: failed to create player job for movie %d: %v", movieID, err)
		return
	}
	m.db.Model(&movie).Update("player_status", models.PlayerStatusPending)
	// Called from workers too (after a transcode finishes): never block one
	// on its own full queue.
	go func() { m.queue <- queuedJob{movieID: movieID, jobType: models.JobTypePlayerTranscode} }()
}

func (m *Manager) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case queued := <-m.queue:
			if queued.jobType == models.JobTypePlayerTranscode {
				m.processPlayer(ctx, queued.movieID)
			} else {
				m.process(ctx, queued.movieID)
			}
		}
	}
}

func (m *Manager) process(ctx context.Context, movieID uint) {
	var movie models.Movie
	if err := m.db.First(&movie, movieID).Error; err != nil {
		log.Printf("transcode: movie %d not found: %v", movieID, err)
		return
	}

	var job models.Job
	if err := m.db.Where("movie_id = ? AND type = ? AND status = ?", movieID, models.JobTypeTranscode, models.JobStatusQueued).
		Order("created_at desc").First(&job).Error; err != nil {
		log.Printf("transcode: no queued job found for movie %d: %v", movieID, err)
		return
	}

	now := time.Now()
	m.db.Model(&job).Updates(map[string]interface{}{"status": models.JobStatusRunning, "started_at": now})
	m.db.Model(&movie).Update("status", models.MovieStatusTranscoding)

	srcPath := filepath.Join(m.moviesDir, movie.PlayableRelpath)
	tmpDir := filepath.Join(m.moviesDir, ".magicboxie", "transcode-tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		m.fail(&job, &movie, fmt.Errorf("creating transcode tmp dir: %w", err))
		return
	}
	tmpOutPath := filepath.Join(tmpDir, fmt.Sprintf("%d.mp4", movie.ID))

	if err := m.runFFmpeg(ctx, &job, &movie, srcPath, tmpOutPath, []string{
		"-vf", "scale=-2:'min(1080,ih)'",
		"-c:v", "libx264",
		"-c:a", "aac",
		"-b:a", "192k",
	}); err != nil {
		os.Remove(tmpOutPath)
		m.fail(&job, &movie, err)
		return
	}

	finalRelpath, err := m.finalize(srcPath, tmpOutPath, movie.SourceRelpath)
	if err != nil {
		m.fail(&job, &movie, fmt.Errorf("finalizing transcode output: %w", err))
		return
	}

	stat, statErr := os.Stat(filepath.Join(m.moviesDir, finalRelpath))

	updates := map[string]interface{}{
		"status":           models.MovieStatusReady,
		"playable_relpath": finalRelpath,
		"video_codec":      "h264",
		"audio_codec":      "aac",
		"container":        "mov,mp4,m4a,3gp,3g2,mj2",
		"error_message":    "",
	}
	if statErr == nil {
		updates["file_size_bytes"] = stat.Size()
	}
	m.db.Model(&movie).Updates(updates)

	finishedAt := time.Now()
	m.db.Model(&job).Updates(map[string]interface{}{
		"status":           models.JobStatusCompleted,
		"progress_percent": 100,
		"finished_at":      finishedAt,
	})

	m.hub.Broadcast(events.Event{Type: "job_completed", Data: eventData{
		"movie_id": movie.ID,
		"job_id":   job.ID,
		"status":   models.MovieStatusReady,
	}})

	m.EnqueuePlayer(movie.ID)
}

// processPlayer encodes a movie's 480p copy for magicboxie-player next to
// (never replacing) its full-quality file. A failure only marks the player
// copy as failed - the movie itself stays ready, and the player falls back
// to downloading the full file and encoding it itself.
func (m *Manager) processPlayer(ctx context.Context, movieID uint) {
	var movie models.Movie
	if err := m.db.First(&movie, movieID).Error; err != nil {
		log.Printf("transcode: movie %d not found: %v", movieID, err)
		return
	}

	var job models.Job
	if err := m.db.Where("movie_id = ? AND type = ? AND status = ?", movieID, models.JobTypePlayerTranscode, models.JobStatusQueued).
		Order("created_at desc").First(&job).Error; err != nil {
		log.Printf("transcode: no queued player job found for movie %d: %v", movieID, err)
		return
	}

	now := time.Now()
	m.db.Model(&job).Updates(map[string]interface{}{"status": models.JobStatusRunning, "started_at": now})

	finalPath := PlayerCopyPath(m.dataDir, movie.ID)
	tmpOutPath := strings.TrimSuffix(finalPath, ".mp4") + ".partial.mp4"
	err := os.MkdirAll(filepath.Dir(finalPath), 0o755)
	if err == nil {
		err = m.runFFmpeg(ctx, &job, &movie, filepath.Join(m.moviesDir, movie.PlayableRelpath), tmpOutPath, PlayerFFmpegArgs)
	}
	if err == nil {
		err = os.Rename(tmpOutPath, finalPath)
	}
	if err != nil {
		os.Remove(tmpOutPath)
		log.Printf("transcode: player copy of movie %d failed: %v", movie.ID, err)
		finishedAt := time.Now()
		m.db.Model(&job).Updates(map[string]interface{}{
			"status":      models.JobStatusFailed,
			"log_tail":    truncate(err.Error(), 4000),
			"finished_at": finishedAt,
		})
		m.db.Model(&movie).Update("player_status", models.PlayerStatusError)
		return
	}

	m.db.Model(&movie).Update("player_status", models.PlayerStatusReady)
	finishedAt := time.Now()
	m.db.Model(&job).Updates(map[string]interface{}{
		"status":           models.JobStatusCompleted,
		"progress_percent": 100,
		"finished_at":      finishedAt,
	})
}

// finalize deletes the (now-superseded) original source file -- per the
// single-file retention model, the transcoded copy is the only file that
// remains -- and moves the transcoded output into place next to where the
// original lived, deduping the filename if needed.
func (m *Manager) finalize(srcPath, tmpOutPath, sourceRelpath string) (finalRelpath string, err error) {
	destDir := filepath.Dir(filepath.Join(m.moviesDir, sourceRelpath))
	base := strings.TrimSuffix(filepath.Base(sourceRelpath), filepath.Ext(sourceRelpath))

	destPath := filepath.Join(destDir, base+".mp4")
	for i := 2; destPath == srcPath; i++ {
		// Extremely rare: transcoded name collides with the still-present
		// source path (e.g. source was already "Title.mp4" but incompatible
		// for codec/resolution reasons). Disambiguate before deleting src.
		destPath = filepath.Join(destDir, fmt.Sprintf("%s (%d).mp4", base, i))
	}

	if err := os.Remove(srcPath); err != nil {
		return "", fmt.Errorf("deleting original source file: %w", err)
	}
	if err := os.Rename(tmpOutPath, destPath); err != nil {
		return "", fmt.Errorf("moving transcoded output into place: %w", err)
	}

	rel, err := filepath.Rel(m.moviesDir, destPath)
	if err != nil {
		return "", err
	}
	return rel, nil
}

func (m *Manager) fail(job *models.Job, movie *models.Movie, cause error) {
	log.Printf("transcode: movie %d failed: %v", movie.ID, cause)
	now := time.Now()
	m.db.Model(job).Updates(map[string]interface{}{
		"status":      models.JobStatusFailed,
		"log_tail":    truncate(cause.Error(), 4000),
		"finished_at": now,
	})
	m.db.Model(movie).Updates(map[string]interface{}{
		"status":        models.MovieStatusError,
		"error_message": cause.Error(),
	})
	m.hub.Broadcast(events.Event{Type: "job_failed", Data: eventData{
		"movie_id": movie.ID,
		"job_id":   job.ID,
		"error":    cause.Error(),
	}})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// eventData is a plain map alias for SSE event payloads, keeping this
// services package free of any dependency on the HTTP/controller layer.
type eventData = map[string]interface{}

func (m *Manager) runFFmpeg(ctx context.Context, job *models.Job, movie *models.Movie, srcPath, outPath string, encodeArgs []string) error {
	args := []string{"-y", "-i", srcPath}
	args = append(args, encodeArgs...)
	args = append(args,
		"-preset", m.preset,
		"-crf", strconv.Itoa(m.crf),
		"-movflags", "+faststart",
		"-progress", "pipe:1",
		"-nostats",
		outPath,
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderrTail strings.Builder
	cmd.Stderr = &tailWriter{limit: 4000, builder: &stderrTail}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}

	m.watchProgress(job, movie, stdout)

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, stderrTail.String())
	}
	return nil
}

// watchProgress reads ffmpeg's `-progress pipe:1` key=value stream and
// updates the job's progress, throttled to roughly once per second so we
// don't hammer SQLite (or the SSE clients) on every frame.
func (m *Manager) watchProgress(job *models.Job, movie *models.Movie, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	lastUpdate := time.Time{}

	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		if key == "out_time" && movie.DurationSeconds > 0 {
			if elapsed, ok := parseFFmpegTime(value); ok {
				percent := (elapsed / movie.DurationSeconds) * 100
				if percent > 100 {
					percent = 100
				}
				if percent < 0 {
					percent = 0
				}

				if time.Since(lastUpdate) >= progressUpdateInterval {
					lastUpdate = time.Now()
					m.db.Model(job).Update("progress_percent", percent)
					m.hub.Broadcast(events.Event{Type: "job_progress", Data: eventData{
						"movie_id":         movie.ID,
						"job_id":           job.ID,
						"progress_percent": percent,
					}})
				}
			}
		}
	}
}

// parseFFmpegTime parses ffmpeg's "-progress" out_time value (HH:MM:SS.ffffff)
// into total seconds.
func parseFFmpegTime(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, err1 := strconv.ParseFloat(parts[0], 64)
	minutes, err2 := strconv.ParseFloat(parts[1], 64)
	seconds, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return hours*3600 + minutes*60 + seconds, true
}

// tailWriter keeps only the last `limit` bytes written to it, for capturing
// a bounded ffmpeg stderr tail on failure.
type tailWriter struct {
	limit   int
	builder *strings.Builder
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.builder.Write(p)
	if w.builder.Len() > w.limit {
		s := w.builder.String()
		w.builder.Reset()
		w.builder.WriteString(s[len(s)-w.limit:])
	}
	return len(p), nil
}

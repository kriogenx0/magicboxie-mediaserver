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
	"syscall"
	"time"

	"gorm.io/gorm"

	"magicboxie/internal/models"
	"magicboxie/internal/services/events"
)

const progressUpdateInterval = 1 * time.Second

// gateCheckInterval is how often a paused or running job re-checks the gate.
const gateCheckInterval = 3 * time.Second

// playerSweepInterval is how often the server looks for ready movies that
// still lack a 480p player copy, so an idle box keeps working through the
// backlog on its own instead of only at startup.
const playerSweepInterval = 10 * time.Minute

// PlayerCopyVersion identifies the current PlayerFFmpegArgs. Bump it when
// the encoding changes: ready copies made with an older version are re-made
// by the sweep, and the player re-downloads them (MagicBoxiePlayerVersion).
const PlayerCopyVersion = 2

// PlayerFFmpegArgs is the video/audio encoding for magicboxie-player's copy:
// progressive (interlaced sources are deinterlaced), fitted inside the
// player's 720x480 output (never upscaled), H.264 Baseline - no CABAC or
// B-frames, the most CPU-expensive parts of decode - which the player's Pi
// Zero can play smoothly. Version 1 kept the height at 480 whatever the
// width, so a 2.39:1 movie came out 1152x480: over twice the pixels the
// screen shows, which the Pi then had to decode and scale down, and played
// choppy.
var PlayerFFmpegArgs = []string{
	"-vf", "bwdif=deint=interlaced,scale=w='min(720,iw)':h='min(480,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2",
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
	threads       int // 0 = ffmpeg's default (all cores)
	crf           int
	maxConcurrent int
	hub           *events.Hub
	gate          *Gate // optional; pauses work during playback or overheating

	queue       chan queuedJob // full transcodes awaiting a worker (always served first)
	syncQueue   chan queuedJob // 480p copies of movies marked for the player: it waits on these, so they go next
	playerQueue chan queuedJob // the rest of the 480p backlog: background work, only run when nothing else is waiting
}

type queuedJob struct {
	movieID uint
	jobType string
}

// PlayerCopyPath is where a movie's 480p copy for magicboxie-player lives.
func PlayerCopyPath(dataDir string, movieID uint) string {
	return filepath.Join(dataDir, "player", fmt.Sprintf("%d.mp4", movieID))
}

// SetGate makes the manager pause work whenever gate says to: new jobs wait
// to start, and a running ffmpeg is suspended (SIGSTOP) until it clears.
func (m *Manager) SetGate(g *Gate) { m.gate = g }

// SetThreads caps the CPU cores each ffmpeg uses, to keep the Pi cooler.
func (m *Manager) SetThreads(n int) { m.threads = n }

// waitForClear blocks while the gate is closed. False means ctx ended.
func (m *Manager) waitForClear(ctx context.Context) bool {
	logged := false
	for {
		reason := m.gate.Reason()
		if reason == "" {
			return true
		}
		if !logged {
			log.Printf("transcode: paused (%s)", reason)
			logged = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(gateCheckInterval):
		}
	}
}

// suspendWhileGated stops/continues the ffmpeg process as the gate closes
// and reopens, until done is closed.
func (m *Manager) suspendWhileGated(proc *os.Process, done <-chan struct{}) {
	ticker := time.NewTicker(gateCheckInterval)
	defer ticker.Stop()
	stopped := false
	for {
		reason := m.gate.Reason()
		if reason != "" && !stopped {
			log.Printf("transcode: pausing ffmpeg (%s)", reason)
			stopped = proc.Signal(syscall.SIGSTOP) == nil
		} else if reason == "" && stopped {
			log.Printf("transcode: resuming ffmpeg")
			_ = proc.Signal(syscall.SIGCONT)
			stopped = false
		}
		select {
		case <-done:
			if stopped {
				_ = proc.Signal(syscall.SIGCONT)
			}
			return
		case <-ticker.C:
		}
	}
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
		syncQueue:     make(chan queuedJob, 4096),
		playerQueue:   make(chan queuedJob, 4096),
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
		if job.Type == models.JobTypePlayerTranscode {
			var movie models.Movie
			m.db.First(&movie, job.MovieID)
			m.playerQueueFor(movie) <- queuedJob{movieID: job.MovieID, jobType: job.Type}
		} else {
			m.queue <- queuedJob{movieID: job.MovieID, jobType: job.Type}
		}
	}

	m.sweepPlayerCopies()
	go func() {
		ticker := time.NewTicker(playerSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sweepPlayerCopies()
			}
		}
	}()
}

// sweepPlayerCopies queues a 480p copy for every ready movie still missing
// one (added before this existed, or its job was lost) or whose copy was made
// by an older PlayerCopyVersion. EnqueuePlayer skips movies that already have
// an active job, so this is safe to repeat.
func (m *Manager) sweepPlayerCopies() {
	var needPlayerCopy []models.Movie
	m.db.Where("status = ? AND (player_status IN ? OR (player_status = ? AND player_version < ?))",
		models.MovieStatusReady, []string{"", models.PlayerStatusPending},
		models.PlayerStatusReady, PlayerCopyVersion).Find(&needPlayerCopy)
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

// playerQueueFor picks the queue for a movie's 480p copy: movies marked
// for the player skip ahead of the library-wide backlog, since the player
// downloads nothing new until its copy is ready.
func (m *Manager) playerQueueFor(movie models.Movie) chan queuedJob {
	if movie.SyncEnabled {
		return m.syncQueue
	}
	return m.playerQueue
}

// EnqueuePlayer queues the 480p copy magicboxie-player downloads, for a
// ready movie that doesn't have a current one yet. A no-op otherwise,
// so it's safe to call whenever a movie might have become eligible. A
// movie just marked for the player whose copy already sits in the backlog
// is moved up to the sync queue (the later backlog entry finds no queued
// job and is skipped).
func (m *Manager) EnqueuePlayer(movieID uint) {
	var movie models.Movie
	if err := m.db.First(&movie, movieID).Error; err != nil {
		return
	}
	if movie.Status != models.MovieStatusReady ||
		(movie.PlayerStatus == models.PlayerStatusReady && movie.PlayerVersion >= PlayerCopyVersion) {
		return
	}
	var active int64
	m.db.Model(&models.Job{}).
		Where("movie_id = ? AND type = ? AND status IN ?", movieID, models.JobTypePlayerTranscode, []string{models.JobStatusQueued, models.JobStatusRunning}).
		Count(&active)
	if active > 0 {
		if movie.SyncEnabled {
			var queued int64
			m.db.Model(&models.Job{}).
				Where("movie_id = ? AND type = ? AND status = ?", movieID, models.JobTypePlayerTranscode, models.JobStatusQueued).
				Count(&queued)
			if queued > 0 {
				go func() { m.syncQueue <- queuedJob{movieID: movieID, jobType: models.JobTypePlayerTranscode} }()
			}
		}
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
	queue := m.playerQueueFor(movie)
	go func() { queue <- queuedJob{movieID: movieID, jobType: models.JobTypePlayerTranscode} }()
}

func (m *Manager) worker(ctx context.Context) {
	run := func(queued queuedJob) {
		if queued.jobType == models.JobTypePlayerTranscode {
			m.processPlayer(ctx, queued.movieID)
		} else {
			m.process(ctx, queued.movieID)
		}
	}
	for {
		if !m.waitForClear(ctx) {
			return
		}
		queued, ok := m.next(ctx)
		if !ok {
			return
		}
		run(queued)
	}
}

// next waits for the next job, by priority: a waiting transcode, then a
// copy the player is waiting on, then the 480p backlog - so a new upload or
// a movie just marked for the player never queues behind hundreds of
// background copies. False means ctx ended.
func (m *Manager) next(ctx context.Context) (queuedJob, bool) {
	select {
	case queued := <-m.queue:
		return queued, true
	default:
	}
	select {
	case queued := <-m.syncQueue:
		return queued, true
	default:
	}
	select {
	case <-ctx.Done():
		return queuedJob{}, false
	case queued := <-m.queue:
		return queued, true
	case queued := <-m.syncQueue:
		return queued, true
	case queued := <-m.playerQueue:
		return queued, true
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
// to downloading and playing the full file.
func (m *Manager) processPlayer(ctx context.Context, movieID uint) {
	var movie models.Movie
	if err := m.db.First(&movie, movieID).Error; err != nil {
		log.Printf("transcode: movie %d not found: %v", movieID, err)
		return
	}

	var job models.Job
	if err := m.db.Where("movie_id = ? AND type = ? AND status = ?", movieID, models.JobTypePlayerTranscode, models.JobStatusQueued).
		Order("created_at desc").Limit(1).Find(&job).Error; err != nil || job.ID == 0 {
		// Already run from the other queue (a movie moved up to the sync
		// queue leaves its backlog entry behind).
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
		m.hub.Broadcast(events.Event{Type: "job_failed", Data: eventData{
			"movie_id": movie.ID,
			"job_id":   job.ID,
			"error":    err.Error(),
		}})
		return
	}

	m.db.Model(&movie).Updates(map[string]interface{}{
		"player_status":  models.PlayerStatusReady,
		"player_version": PlayerCopyVersion,
	})
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
	if m.threads > 0 {
		args = append(args, "-threads", strconv.Itoa(m.threads))
	}
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

	done := make(chan struct{})
	if m.gate != nil {
		go m.suspendWhileGated(cmd.Process, done)
	}
	m.watchProgress(job, movie, stdout)
	close(done)

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
						"job_type":         job.Type,
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

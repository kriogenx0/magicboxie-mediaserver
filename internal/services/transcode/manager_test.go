package transcode

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"magicboxie/internal/db"
	"magicboxie/internal/models"
)

func TestSweepRemakesPlayerCopiesFromOlderVersions(t *testing.T) {
	gdb, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stale := models.Movie{Title: "Moana", OriginalFilename: "a.mkv", SourceRelpath: "a.mkv",
		Status: models.MovieStatusReady, PlayerStatus: models.PlayerStatusReady, PlayerVersion: PlayerCopyVersion - 1}
	current := models.Movie{Title: "Up", OriginalFilename: "b.mkv", SourceRelpath: "b.mkv",
		Status: models.MovieStatusReady, PlayerStatus: models.PlayerStatusReady, PlayerVersion: PlayerCopyVersion}
	gdb.Create(&stale)
	gdb.Create(&current)

	m := NewManager(gdb, t.TempDir(), t.TempDir(), "veryfast", 23, 1, nil)
	m.sweepPlayerCopies()

	gdb.First(&stale, stale.ID)
	gdb.First(&current, current.ID)
	if stale.PlayerStatus != models.PlayerStatusPending {
		t.Errorf("stale copy status = %q, want pending", stale.PlayerStatus)
	}
	if current.PlayerStatus != models.PlayerStatusReady {
		t.Errorf("current copy status = %q, want ready", current.PlayerStatus)
	}
}

func TestPlayerCopyFitsThePlayerScreen(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, tc := range []struct{ src, want string }{
		{"1920x800", "720x300"},  // 2.39:1 scope
		{"1920x1080", "720x406"}, // 16:9
		{"1440x1080", "640x480"}, // 4:3
		{"640x360", "640x360"},   // never upscaled
	} {
		dir := t.TempDir()
		src, out := filepath.Join(dir, "src.mp4"), filepath.Join(dir, "out.mp4")
		if b, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=s="+tc.src+":r=24",
			"-t", "0.2", src).CombinedOutput(); err != nil {
			t.Fatalf("making source: %v: %s", err, b)
		}
		args := append([]string{"-loglevel", "error", "-i", src}, PlayerFFmpegArgs...)
		if b, err := exec.Command("ffmpeg", append(args, out)...).CombinedOutput(); err != nil {
			t.Fatalf("encoding %s: %v: %s", tc.src, err, b)
		}
		b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", out).Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(b)); got != tc.want {
			t.Errorf("%s -> %s, want %s", tc.src, got, tc.want)
		}
	}
}

func TestCopiesForThePlayerGoBeforeTheBacklog(t *testing.T) {
	gdb, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backlog := models.Movie{Title: "Moana", OriginalFilename: "a.mkv", SourceRelpath: "a.mkv", Status: models.MovieStatusReady}
	later := models.Movie{Title: "Up", OriginalFilename: "b.mkv", SourceRelpath: "b.mkv", Status: models.MovieStatusReady}
	synced := models.Movie{Title: "Cars", OriginalFilename: "c.mkv", SourceRelpath: "c.mkv", Status: models.MovieStatusReady, SyncEnabled: true}
	gdb.Create(&backlog)
	gdb.Create(&later)
	gdb.Create(&synced)

	m := NewManager(gdb, t.TempDir(), t.TempDir(), "veryfast", 23, 1, nil)
	m.EnqueuePlayer(backlog.ID)
	m.EnqueuePlayer(later.ID)
	m.EnqueuePlayer(synced.ID)
	// Marked for the player after its copy was already in the backlog.
	gdb.Model(&later).Update("sync_enabled", true)
	m.EnqueuePlayer(later.ID)

	ctx := context.Background()
	waitFor := func(ch chan queuedJob, n int) {
		deadline := time.Now().Add(2 * time.Second)
		for len(ch) < n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor(m.syncQueue, 2)
	waitFor(m.playerQueue, 2)

	first, _ := m.next(ctx)
	second, _ := m.next(ctx)
	got := map[uint]bool{first.movieID: true, second.movieID: true}
	if !got[synced.ID] || !got[later.ID] {
		t.Errorf("first two jobs = %d, %d; want the movies marked for the player (%d, %d)",
			first.movieID, second.movieID, synced.ID, later.ID)
	}
	third, _ := m.next(ctx)
	if third.movieID != backlog.ID && third.movieID != later.ID {
		t.Errorf("third job = %d, want a backlog movie", third.movieID)
	}
}

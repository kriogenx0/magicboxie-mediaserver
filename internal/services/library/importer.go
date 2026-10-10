package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"

	"magicboxie/internal/models"
	"magicboxie/internal/services/media"
	"magicboxie/internal/services/mediatype"
	"magicboxie/internal/services/thumbnail"
	"magicboxie/internal/services/tmdb"
)

type Importer struct {
	db        *gorm.DB
	moviesDir string
	dataDir   string
	tmdb      *tmdb.Client

	// OnNeedsTranscode is invoked (if set) whenever an imported movie turns
	// out not to be browser/iOS compatible, so the transcode worker pool can
	// pick it up. Wired from main.go to avoid this package depending on the
	// transcode package directly.
	OnNeedsTranscode func(movieID uint)

	// OnReady is invoked (if set) when an imported movie is playable as-is,
	// so its 480p player copy can be made. Movies that need a transcode get
	// theirs once that finishes instead.
	OnReady func(movieID uint)
}

func NewImporter(db *gorm.DB, moviesDir, dataDir string, tmdbClient *tmdb.Client) *Importer {
	return &Importer{db: db, moviesDir: moviesDir, dataDir: dataDir, tmdb: tmdbClient}
}

// Scan walks moviesDir for video files not already known (by SourceRelpath)
// and imports each one, then retries anything left stuck at "probing" or
// "error" from an earlier interrupted run (see RetryStuck) - a plain
// directory walk alone would never touch those again, since they already
// have a row. Errors on individual files are logged and skipped rather
// than aborting the whole scan.
func (im *Importer) Scan(ctx context.Context) (imported int, err error) {
	if _, retryErr := im.RetryStuck(ctx); retryErr != nil {
		log.Printf("library: retrying stuck movies failed: %v", retryErr)
	}

	err = filepath.WalkDir(im.moviesDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !mediatype.IsMovie(d.Name()) {
			return nil
		}

		relpath, err := filepath.Rel(im.moviesDir, path)
		if err != nil {
			return nil
		}

		didImport, err := im.ImportNewFile(ctx, relpath, path)
		if err != nil {
			log.Printf("library: failed to import %q: %v", relpath, err)
			return nil
		}
		if didImport {
			imported++
		}
		return nil
	})
	return imported, err
}

// ImportNewFile imports absPath (whose path relative to moviesDir is
// relpath) if it isn't already known by SourceRelpath. Returns whether an
// import actually happened. Used by both the directory scanner and the
// chunked-upload completion handler.
func (im *Importer) ImportNewFile(ctx context.Context, relpath, absPath string) (bool, error) {
	var count int64
	im.db.Model(&models.Movie{}).Where("source_relpath = ?", relpath).Count(&count)
	if count > 0 {
		return false, nil
	}
	if err := im.importFile(ctx, relpath, absPath); err != nil {
		return false, err
	}
	return true, nil
}

func (im *Importer) importFile(ctx context.Context, relpath, absPath string) error {
	filename := filepath.Base(absPath)
	title, year := ParseFilename(filename)

	movie := &models.Movie{
		Title:            title,
		Year:             year,
		OriginalFilename: filename,
		SourceRelpath:    relpath,
		PlayableRelpath:  relpath,
		Status:           models.MovieStatusProbing,
	}
	if stat, err := os.Stat(absPath); err == nil {
		movie.FileSizeBytes = stat.Size()
	}
	if err := im.db.Create(movie).Error; err != nil {
		return fmt.Errorf("creating movie row: %w", err)
	}

	return im.probeAndFinalize(ctx, movie, absPath, title, year)
}

// probeAndFinalize runs ffprobe + TMDB matching against movie's file and
// saves the result - status ends at "ready"/"needs_transcode" on success or
// "error" (with error_message) if ffprobe itself fails. Shared by
// importFile (a freshly-created row) and RetryStuck (an existing row an
// earlier run never got this far for).
func (im *Importer) probeAndFinalize(ctx context.Context, movie *models.Movie, absPath, title string, year int) error {
	info, err := media.Probe(ctx, absPath)
	if err != nil {
		im.db.Model(movie).Updates(map[string]interface{}{
			"status":        models.MovieStatusError,
			"error_message": err.Error(),
		})
		return err
	}

	movie.DurationSeconds = info.DurationSeconds
	movie.VideoCodec = info.VideoCodec
	movie.AudioCodec = info.AudioCodec
	movie.Container = info.Container

	// Only match if nothing has claimed this movie yet: a stuck-at-probing
	// row can already carry a correct match from a later rematch-tmdb run
	// (rematch-tmdb only ever touches metadata, never status - that's
	// exactly why it can be stuck despite looking fully matched). Redoing
	// the search here isn't just wasteful in that case, it's actively
	// destructive - confirmed live, it silently replaced a correct match
	// with a wrong one for a title ambiguous enough to rank differently on
	// a second search.
	if movie.TMDBID == nil {
		im.matchMetadata(ctx, movie, absPath, title, year)
	}

	if info.IsBrowserCompatible() {
		movie.Status = models.MovieStatusReady
	} else {
		movie.Status = models.MovieStatusNeedsTranscode
	}

	if err := im.db.Save(movie).Error; err != nil {
		return err
	}

	if movie.Status == models.MovieStatusNeedsTranscode && im.OnNeedsTranscode != nil {
		im.OnNeedsTranscode(movie.ID)
	}
	if movie.Status == models.MovieStatusReady && im.OnReady != nil {
		im.OnReady(movie.ID)
	}
	return nil
}

// RetryStuck finds every movie left at "probing" or "error" - an import
// that was interrupted (crash, restart, ...) before reaching a terminal
// status, or one ffprobe failed on transiently - and, for any whose file
// still exists, retries the probe+match that importFile itself never
// finished. A regular Scan alone can never fix these: ImportNewFile only
// ever looks at files with no existing row at all, so a row stuck exactly
// like this is invisible to it forever otherwise. Returns how many it
// retried; per-file errors are logged and skipped, matching Scan's own
// one-bad-file-shouldn't-stop-the-rest behavior.
func (im *Importer) RetryStuck(ctx context.Context) (retried int, err error) {
	var movies []models.Movie
	if dbErr := im.db.Where("status IN ?", []string{models.MovieStatusProbing, models.MovieStatusError}).
		Find(&movies).Error; dbErr != nil {
		return 0, dbErr
	}
	for i := range movies {
		movie := &movies[i]
		absPath := filepath.Join(im.moviesDir, movie.SourceRelpath)
		if _, statErr := os.Stat(absPath); statErr != nil {
			continue
		}
		if probeErr := im.probeAndFinalize(ctx, movie, absPath, movie.Title, movie.Year); probeErr != nil {
			log.Printf("library: retrying stuck movie %q failed: %v", movie.Title, probeErr)
			continue
		}
		retried++
	}
	return retried, nil
}

// ApplyManualMatch lets a user correct an ambiguous/no-match import by
// picking the right TMDB entry themselves (POST /movies/{id}/match).
func (im *Importer) ApplyManualMatch(ctx context.Context, movie *models.Movie, tmdbID int) error {
	details, err := im.tmdb.GetMovieDetails(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("fetching tmdb details: %w", err)
	}
	im.applyTMDBDetails(ctx, movie, details)
	movie.NeedsReview = false
	return im.db.Save(movie).Error
}

// SearchTMDB looks up candidate TMDB matches by title (+ optional year), for
// manually correcting an ambiguous or missing automatic match in the UI.
func (im *Importer) SearchTMDB(ctx context.Context, title string, year int) ([]tmdb.SearchResult, error) {
	return im.tmdb.SearchMovie(ctx, title, year)
}

// EnsureThumbnailCandidates lazily migrates a legacy animated poster to the
// JPEG still-frame picker, avoiding a burst of ffmpeg work across the entire
// existing library.
func (im *Importer) EnsureThumbnailCandidates(ctx context.Context, movie *models.Movie) error {
	candidatePath := filepath.Join(im.dataDir, "images", "thumbnails", fmt.Sprintf("%d", movie.ID), "0.jpg")
	if _, err := os.Stat(candidatePath); err == nil {
		if strings.EqualFold(filepath.Ext(movie.PosterPath), ".jpg") {
			return nil
		}
		middleCandidate := filepath.Join(im.dataDir, "images", "thumbnails", fmt.Sprintf("%d", movie.ID), "2.jpg")
		posterPath := filepath.Join(im.dataDir, "images", "posters", fmt.Sprintf("%d.jpg", movie.ID))
		if err := copyFile(middleCandidate, posterPath); err != nil {
			return err
		}
		movie.PosterPath = fmt.Sprintf("posters/%d.jpg", movie.ID)
		movie.PosterIsGenerated = true
		return im.db.Save(movie).Error
	}

	absPath := filepath.Join(im.moviesDir, movie.SourceRelpath)
	im.generateFallbackThumbnail(ctx, movie, absPath)
	if _, err := os.Stat(candidatePath); err != nil {
		return fmt.Errorf("generating thumbnail candidates: %w", err)
	}
	return im.db.Save(movie).Error
}

func (im *Importer) matchMetadata(ctx context.Context, movie *models.Movie, absPath, title string, year int) {
	results, err := im.tmdb.SearchMovie(ctx, title, year)
	if err != nil && !errors.Is(err, tmdb.ErrNotConfigured) {
		log.Printf("library: tmdb search failed for %q: %v", title, err)
	}
	if len(results) == 0 && year > 0 {
		// Retry without the year in case it was parsed wrong or TMDB's
		// primary_release_year filter is too strict for this title.
		results, _ = im.tmdb.SearchMovie(ctx, title, 0)
	}

	if len(results) == 0 {
		im.generateFallbackThumbnail(ctx, movie, absPath)
		return
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Popularity > results[j].Popularity })
	best := results[0]
	movie.NeedsReview = year == 0 || len(results) > 1

	details, err := im.tmdb.GetMovieDetails(ctx, best.ID)
	if err != nil {
		log.Printf("library: tmdb details fetch failed for %q (id=%d): %v", title, best.ID, err)
		im.generateFallbackThumbnail(ctx, movie, absPath)
		return
	}

	im.applyTMDBDetails(ctx, movie, details)
}

func (im *Importer) applyTMDBDetails(ctx context.Context, movie *models.Movie, details *tmdb.MovieDetails) {
	tmdbID := details.ID
	movie.TMDBID = &tmdbID
	if strings.TrimSpace(details.Title) != "" {
		movie.Title = details.Title
	}
	movie.Overview = details.Overview
	movie.RuntimeMinutes = details.Runtime

	genres := make([]string, 0, len(details.Genres))
	for _, g := range details.Genres {
		genres = append(genres, g.Name)
	}
	if b, err := json.Marshal(genres); err == nil {
		movie.GenresJSON = string(b)
	}

	cast := details.Credits.Cast
	if len(cast) > 10 {
		cast = cast[:10]
	}
	if b, err := json.Marshal(cast); err == nil {
		movie.CastJSON = string(b)
	}

	posterDest := filepath.Join(im.dataDir, "images", "posters", fmt.Sprintf("%d.jpg", movie.ID))
	if err := cacheImage(ctx, im.tmdb, "w780", details.PosterPath, posterDest); err != nil {
		log.Printf("library: poster download failed for movie %d: %v", movie.ID, err)
	} else if details.PosterPath != "" {
		movie.PosterPath = fmt.Sprintf("posters/%d.jpg", movie.ID)
		movie.PosterIsGenerated = false
	}

	backdropDest := filepath.Join(im.dataDir, "images", "backdrops", fmt.Sprintf("%d.jpg", movie.ID))
	if err := cacheImage(ctx, im.tmdb, "w1280", details.BackdropPath, backdropDest); err != nil {
		log.Printf("library: backdrop download failed for movie %d: %v", movie.ID, err)
	} else if details.BackdropPath != "" {
		movie.BackdropPath = fmt.Sprintf("backdrops/%d.jpg", movie.ID)
	}
}

func (im *Importer) generateFallbackThumbnail(ctx context.Context, movie *models.Movie, absPath string) {
	movie.NeedsReview = true
	candidateDir := filepath.Join(im.dataDir, "images", "thumbnails", fmt.Sprintf("%d", movie.ID))
	candidates, err := thumbnail.GenerateCandidates(ctx, absPath, movie.DurationSeconds, candidateDir)
	if err != nil {
		log.Printf("library: thumbnail generation failed for movie %d: %v", movie.ID, err)
		return
	}

	// Start with the middle frame. The user can choose any candidate from the
	// movie detail page without re-running ffmpeg.
	destPath := filepath.Join(im.dataDir, "images", "posters", fmt.Sprintf("%d.jpg", movie.ID))
	if err := copyFile(candidates[len(candidates)/2].Path, destPath); err != nil {
		log.Printf("library: selecting initial thumbnail failed for movie %d: %v", movie.ID, err)
		return
	}
	movie.PosterPath = fmt.Sprintf("posters/%d.jpg", movie.ID)
	movie.PosterIsGenerated = true
}

func copyFile(sourcePath, destPath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0o644)
}

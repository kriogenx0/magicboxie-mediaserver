package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"magicboxie/internal/models"
	"magicboxie/internal/services/library"
	"magicboxie/internal/services/music"
	"magicboxie/internal/services/tmdb"
)

type ItemsController struct {
	db            *gorm.DB
	importer      *library.Importer
	musicImporter *music.Importer
	moviesDir     string
	dataDir       string
}

func NewItemsController(db *gorm.DB, importer *library.Importer, musicImporter *music.Importer, moviesDir, dataDir string) *ItemsController {
	return &ItemsController{db: db, importer: importer, musicImporter: musicImporter, moviesDir: moviesDir, dataDir: dataDir}
}

// ---- Jellyfin response shapes ----
//
// Fields are PascalCase to match real Jellyfin's wire format exactly (see
// magicboxie-appletv's JellyfinItem.swift CodingKeys). The MagicBoxie*-prefixed
// fields are additive extensions real/generic Jellyfin clients simply
// ignore (unknown JSON keys are dropped by Codable), while MagicBoxie-aware
// clients (the web frontend, magicboxie-device's home-sync) read them for
// status/progress/original-filename that plain Jellyfin has no concept of.
//
// Movies, artists, albums, and tracks each have their own auto-incrementing
// primary key starting at 1, so a bare numeric Id would collide across
// types. Item ids are instead "<kind>-<n>" (e.g. "movie-5", "track-12");
// clients treat Id as an opaque string already (they only ever echo it back
// into another URL), so this is invisible to them.

type jellyfinPerson struct {
	Id   string `json:"Id"`
	Name string `json:"Name"`
	Type string `json:"Type"`
	Role string `json:"Role,omitempty"`
}

type jellyfinMediaStream struct {
	Type  string `json:"Type"`
	Codec string `json:"Codec"`
}

type jellyfinItem struct {
	Id                string                `json:"Id"`
	Name              string                `json:"Name"`
	Type              string                `json:"Type"`
	CollectionType    string                `json:"CollectionType,omitempty"`
	IsFolder          bool                  `json:"IsFolder,omitempty"`
	Overview          string                `json:"Overview,omitempty"`
	ProductionYear    int                   `json:"ProductionYear,omitempty"`
	RunTimeTicks      int64                 `json:"RunTimeTicks,omitempty"`
	DateCreated       string                `json:"DateCreated,omitempty"`
	Genres            []string              `json:"Genres,omitempty"`
	ImageTags         map[string]string     `json:"ImageTags,omitempty"`
	BackdropImageTags []string              `json:"BackdropImageTags,omitempty"`
	People            []jellyfinPerson      `json:"People,omitempty"`
	ProviderIds       map[string]string     `json:"ProviderIds,omitempty"`
	MediaStreams      []jellyfinMediaStream `json:"MediaStreams,omitempty"`

	// Music fields (real Jellyfin field names)
	AlbumArtist       string `json:"AlbumArtist,omitempty"`
	Album             string `json:"Album,omitempty"`
	IndexNumber       int    `json:"IndexNumber,omitempty"`
	ParentIndexNumber int    `json:"ParentIndexNumber,omitempty"`

	MagicBoxieStatus            string   `json:"MagicBoxieStatus"`
	MagicBoxieProgressPercent   *float64 `json:"MagicBoxieProgressPercent,omitempty"`
	MagicBoxieOriginalFilename  string   `json:"MagicBoxieOriginalFilename"`
	MagicBoxieNeedsReview       bool     `json:"MagicBoxieNeedsReview"`
	MagicBoxiePosterIsGenerated bool     `json:"MagicBoxiePosterIsGenerated"`
	MagicBoxieSyncEnabled       bool     `json:"MagicBoxieSyncEnabled"`
}

type itemsResponse struct {
	Items            []jellyfinItem `json:"Items"`
	TotalRecordCount int            `json:"TotalRecordCount"`
	StartIndex       int            `json:"StartIndex"`
}

// ticksPerSecond converts seconds to Jellyfin's RunTimeTicks unit (100ns).
const ticksPerSecond = 10_000_000

func formatItemID(kind string, id uint) string {
	return fmt.Sprintf("%s-%d", kind, id)
}

// parseItemID splits a "<kind>-<n>" item id. ok is false if the id doesn't
// match the expected kind or isn't a valid id at all.
func parseItemID(raw, wantKind string) (id uint, ok bool) {
	kind, numStr, found := strings.Cut(raw, "-")
	if !found || kind != wantKind {
		return 0, false
	}
	n, err := strconv.ParseUint(numStr, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(n), true
}

func movieToItem(m models.Movie) jellyfinItem {
	item := jellyfinItem{
		Id:                          formatItemID("movie", m.ID),
		Name:                        m.Title,
		Type:                        "Movie",
		Overview:                    m.Overview,
		ProductionYear:              m.Year,
		RunTimeTicks:                int64(m.DurationSeconds * ticksPerSecond),
		DateCreated:                 m.AddedAt.UTC().Format(time.RFC3339),
		MagicBoxieStatus:            m.Status,
		MagicBoxieOriginalFilename:  m.OriginalFilename,
		MagicBoxieNeedsReview:       m.NeedsReview,
		MagicBoxiePosterIsGenerated: m.PosterIsGenerated,
		MagicBoxieSyncEnabled:       m.SyncEnabled,
	}

	if m.GenresJSON != "" {
		_ = json.Unmarshal([]byte(m.GenresJSON), &item.Genres)
	}

	var cast []tmdb.CastMember
	if m.CastJSON != "" {
		_ = json.Unmarshal([]byte(m.CastJSON), &cast)
		for _, c := range cast {
			item.People = append(item.People, jellyfinPerson{Name: c.Name, Type: "Actor", Role: c.Character})
		}
	}

	if m.TMDBID != nil {
		item.ProviderIds = map[string]string{"Tmdb": strconv.Itoa(*m.TMDBID)}
	}

	tag := strconv.FormatInt(m.UpdatedAt.Unix(), 10)
	if m.PosterPath != "" {
		item.ImageTags = map[string]string{"Primary": tag}
	}
	if m.BackdropPath != "" {
		item.BackdropImageTags = []string{tag}
	}

	if m.VideoCodec != "" {
		item.MediaStreams = append(item.MediaStreams, jellyfinMediaStream{Type: "Video", Codec: m.VideoCodec})
	}
	if m.AudioCodec != "" {
		item.MediaStreams = append(item.MediaStreams, jellyfinMediaStream{Type: "Audio", Codec: m.AudioCodec})
	}

	return item
}

func artistToItem(a models.Artist) jellyfinItem {
	item := jellyfinItem{
		Id:   formatItemID("artist", a.ID),
		Name: a.Name,
		Type: "MusicArtist",
	}
	if a.ImagePath != "" {
		item.ImageTags = map[string]string{"Primary": "1"}
	}
	return item
}

func albumToItem(al models.Album, artistName string) jellyfinItem {
	item := jellyfinItem{
		Id:             formatItemID("album", al.ID),
		Name:           al.Title,
		Type:           "MusicAlbum",
		ProductionYear: al.Year,
		AlbumArtist:    artistName,
	}
	if al.CoverPath != "" {
		item.ImageTags = map[string]string{"Primary": strings.ReplaceAll(al.CoverPath, "/", "-")}
	}
	return item
}

func trackToItem(t models.Track, albumTitle, artistName string) jellyfinItem {
	item := jellyfinItem{
		Id:                formatItemID("track", t.ID),
		Name:              t.Title,
		Type:              "Audio",
		RunTimeTicks:      int64(t.DurationSeconds * ticksPerSecond),
		Album:             albumTitle,
		AlbumArtist:       artistName,
		IndexNumber:       t.TrackNumber,
		ParentIndexNumber: t.DiscNumber,
		MagicBoxieStatus:  models.MovieStatusReady, // tracks need no compatibility processing
	}
	if t.Codec != "" {
		item.MediaStreams = append(item.MediaStreams, jellyfinMediaStream{Type: "Audio", Codec: t.Codec})
	}
	return item
}

// Views returns the fixed top-level libraries ("Movies", "Music") a
// Jellyfin client browses into via ParentId. CollectionType matters beyond
// cosmetics: e.g. Swiftfin's home screen builds a "Latest in <library>" row
// only for views whose CollectionType is one of a known set (movies,
// tvshows, musicvideos, homevideos) -- unlike its generic library browser,
// it does not fall back to treating a missing CollectionType as a plain
// folder, so omitting it silently hides the library from that screen even
// though login and manual browsing still work.
func (ic *ItemsController) Views(c *gin.Context) {
	c.JSON(http.StatusOK, itemsResponse{
		Items: []jellyfinItem{
			{Id: "movies", Name: "Movies", Type: "CollectionFolder", CollectionType: "movies", IsFolder: true},
			{Id: "music", Name: "Music", Type: "CollectionFolder", CollectionType: "music", IsFolder: true},
		},
		TotalRecordCount: 2,
	})
}

// List handles GET /Users/{userId}/Items, branching on IncludeItemTypes to
// serve movies, artists, albums, or tracks (with ParentId scoping the
// artist->albums and album->tracks hierarchy).
func (ic *ItemsController) List(c *gin.Context) {
	switch c.Query("IncludeItemTypes") {
	case "MusicArtist":
		ic.listArtists(c)
	case "MusicAlbum":
		ic.listAlbums(c)
	case "Audio":
		ic.listTracks(c)
	default:
		ic.listMovies(c)
	}
}

func (ic *ItemsController) listMovies(c *gin.Context) {
	var movies []models.Movie
	if err := ic.db.Order("added_at desc").Find(&movies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list items"})
		return
	}
	items := make([]jellyfinItem, len(movies))
	for i, m := range movies {
		items[i] = movieToItem(m)
	}
	c.JSON(http.StatusOK, itemsResponse{Items: items, TotalRecordCount: len(items)})
}

func (ic *ItemsController) listArtists(c *gin.Context) {
	var artists []models.Artist
	if err := ic.db.Order("name asc").Find(&artists).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list artists"})
		return
	}
	items := make([]jellyfinItem, len(artists))
	for i, a := range artists {
		items[i] = artistToItem(a)
	}
	c.JSON(http.StatusOK, itemsResponse{Items: items, TotalRecordCount: len(items)})
}

func (ic *ItemsController) listAlbums(c *gin.Context) {
	query := ic.db.Order("title asc")
	if parentID := c.Query("ParentId"); parentID != "" {
		artistID, ok := parseItemID(parentID, "artist")
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ParentId"})
			return
		}
		query = query.Where("artist_id = ?", artistID)
	}

	var albums []models.Album
	if err := query.Find(&albums).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list albums"})
		return
	}

	items := make([]jellyfinItem, len(albums))
	for i, al := range albums {
		items[i] = albumToItem(al, ic.artistName(al.ArtistID))
	}
	c.JSON(http.StatusOK, itemsResponse{Items: items, TotalRecordCount: len(items)})
}

func (ic *ItemsController) listTracks(c *gin.Context) {
	query := ic.db.Order("disc_number asc, track_number asc")
	if parentID := c.Query("ParentId"); parentID != "" {
		albumID, ok := parseItemID(parentID, "album")
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ParentId"})
			return
		}
		query = query.Where("album_id = ?", albumID)
	}

	var tracks []models.Track
	if err := query.Find(&tracks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list tracks"})
		return
	}

	items := make([]jellyfinItem, len(tracks))
	for i, t := range tracks {
		albumTitle, artistName := ic.albumAndArtistName(t.AlbumID)
		items[i] = trackToItem(t, albumTitle, artistName)
	}
	c.JSON(http.StatusOK, itemsResponse{Items: items, TotalRecordCount: len(items)})
}

func (ic *ItemsController) artistName(artistID uint) string {
	var artist models.Artist
	if err := ic.db.First(&artist, artistID).Error; err != nil {
		return ""
	}
	return artist.Name
}

func (ic *ItemsController) albumAndArtistName(albumID uint) (albumTitle, artistName string) {
	var album models.Album
	if err := ic.db.First(&album, albumID).Error; err != nil {
		return "", ""
	}
	return album.Title, ic.artistName(album.ArtistID)
}

// Latest returns a bare array (not the paging envelope) of the most
// recently-ready movies, matching magicboxie-appletv's
// MovieService.fetchLatestMovies() expectation exactly.
func (ic *ItemsController) Latest(c *gin.Context) {
	limit := 20
	if v, err := strconv.Atoi(c.Query("Limit")); err == nil && v > 0 {
		limit = v
	}

	var movies []models.Movie
	if err := ic.db.Where("status = ?", models.MovieStatusReady).
		Order("added_at desc").Limit(limit).Find(&movies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list latest items"})
		return
	}
	items := make([]jellyfinItem, len(movies))
	for i, m := range movies {
		items[i] = movieToItem(m)
	}
	c.JSON(http.StatusOK, items)
}

// Detail handles GET /Users/{userId}/Items/{itemId} for any item kind.
func (ic *ItemsController) Detail(c *gin.Context) {
	raw := c.Param("itemId")
	kind, _, _ := strings.Cut(raw, "-")

	switch kind {
	case "artist":
		artist, ok := ic.loadArtist(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, artistToItem(artist))
	case "album":
		album, ok := ic.loadAlbum(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, albumToItem(album, ic.artistName(album.ArtistID)))
	case "track":
		track, ok := ic.loadTrack(c)
		if !ok {
			return
		}
		albumTitle, artistName := ic.albumAndArtistName(track.AlbumID)
		c.JSON(http.StatusOK, trackToItem(track, albumTitle, artistName))
	default:
		movie, ok := ic.loadMovie(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, movieToItem(movie))
	}
}

type renameMovieRequest struct {
	Title string `json:"title"`
}

func (ic *ItemsController) Rename(c *gin.Context) {
	id, ok := parseItemID(c.Param("itemId"), "movie")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid movie id"})
		return
	}
	var req renameMovieRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title must be between 1 and 200 characters"})
		return
	}
	var movie models.Movie
	if err := ic.db.First(&movie, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "movie not found"})
		return
	}
	if err := ic.db.Model(&movie).Update("title", req.Title).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename movie"})
		return
	}
	movie.Title = req.Title
	c.JSON(http.StatusOK, movieToItem(movie))
}

func (ic *ItemsController) Delete(c *gin.Context) {
	id, ok := parseItemID(c.Param("itemId"), "movie")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid movie id"})
		return
	}
	var movie models.Movie
	if err := ic.db.First(&movie, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "movie not found"})
		return
	}
	mediaPath := filepath.Join(ic.moviesDir, movie.PlayableRelpath)
	if movie.ContentSHA256 != "" {
		ic.db.Delete(&models.MediaChecksum{}, "sha256 = ?", movie.ContentSHA256)
	}
	if err := ic.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&models.Job{}, "movie_id = ?", movie.ID).Error; err != nil {
			return err
		}
		return tx.Delete(&movie).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete movie"})
		return
	}
	if movie.PlayableRelpath != "" {
		_ = os.Remove(mediaPath)
	}
	_ = os.RemoveAll(filepath.Join(ic.dataDir, "images", "thumbnails", fmt.Sprint(movie.ID)))
	_ = os.Remove(filepath.Join(ic.dataDir, "images", "posters", fmt.Sprintf("%d.jpg", movie.ID)))
	_ = os.Remove(filepath.Join(ic.dataDir, "images", "backdrops", fmt.Sprintf("%d.jpg", movie.ID)))
	_ = os.Remove(filepath.Join(ic.dataDir, "previews", fmt.Sprintf("%d.mp4", movie.ID)))
	c.Status(http.StatusNoContent)
}

func (ic *ItemsController) PlaybackInfo(c *gin.Context) {
	raw := c.Param("itemId")
	kind, _, _ := strings.Cut(raw, "-")

	if kind == "track" {
		track, ok := ic.loadTrack(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"MediaSources": []gin.H{{
				"Id":                   formatItemID("track", track.ID),
				"Path":                 track.FileRelpath,
				"Container":            track.Codec,
				"SupportsDirectPlay":   true,
				"SupportsDirectStream": true,
			}},
			"PlaySessionId": uuid.NewString(),
		})
		return
	}

	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	ready := movie.Status == models.MovieStatusReady
	c.JSON(http.StatusOK, gin.H{
		"MediaSources": []gin.H{{
			"Id":                   formatItemID("movie", movie.ID),
			"Path":                 movie.OriginalFilename,
			"Container":            "mp4",
			"SupportsDirectPlay":   ready,
			"SupportsDirectStream": ready,
			"Bitrate":              4000000,
		}},
		"PlaySessionId": uuid.NewString(),
	})
}

// PrimaryImage serves the poster for a movie or the cover art for an album.
// Artists/tracks have no image of their own in the initial implementation.
func (ic *ItemsController) PrimaryImage(c *gin.Context) {
	raw := c.Param("itemId")
	kind, _, _ := strings.Cut(raw, "-")

	if kind == "album" {
		album, ok := ic.loadAlbum(c)
		if !ok {
			return
		}
		if album.CoverPath == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "no cover art"})
			return
		}
		serveImageFile(c, filepath.Join(ic.dataDir, "images", album.CoverPath))
		return
	}

	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	if movie.PosterIsGenerated && !strings.EqualFold(filepath.Ext(movie.PosterPath), ".jpg") {
		if err := ic.importer.EnsureThumbnailCandidates(c.Request.Context(), &movie); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to migrate generated poster"})
			return
		}
	}
	if movie.PosterPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no poster"})
		return
	}
	serveImageFile(c, filepath.Join(ic.dataDir, "images", movie.PosterPath))
}

func (ic *ItemsController) BackdropImage(c *gin.Context) {
	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	if movie.BackdropPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no backdrop"})
		return
	}
	serveImageFile(c, filepath.Join(ic.dataDir, "images", movie.BackdropPath))
}

// ThumbnailCandidateImage serves one of the still frames generated when no
// metadata poster was available. Like other image routes, it is public so an
// <img> element does not need to attach an auth header.
func (ic *ItemsController) ThumbnailCandidateImage(c *gin.Context) {
	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 || index > 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid thumbnail index"})
		return
	}
	serveImageFile(c, filepath.Join(ic.dataDir, "images", "thumbnails", fmt.Sprintf("%d", movie.ID), fmt.Sprintf("%d.jpg", index)))
}

// ThumbnailCandidates lists the still frames available for a generated
// poster. Missing files are omitted so partially generated legacy data fails
// gracefully.
func (ic *ItemsController) ThumbnailCandidates(c *gin.Context) {
	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	if movie.PosterIsGenerated {
		if err := ic.importer.EnsureThumbnailCandidates(c.Request.Context(), &movie); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	selectedPoster, _ := os.ReadFile(filepath.Join(ic.dataDir, "images", movie.PosterPath))
	candidates := make([]gin.H, 0, 5)
	for index := 0; index < 5; index++ {
		path := filepath.Join(ic.dataDir, "images", "thumbnails", fmt.Sprintf("%d", movie.ID), fmt.Sprintf("%d.jpg", index))
		if candidateData, err := os.ReadFile(path); err == nil {
			candidates = append(candidates, gin.H{
				"index":    index,
				"url":      fmt.Sprintf("/Items/movie-%d/Images/Thumbnail/%d", movie.ID, index),
				"selected": bytes.Equal(selectedPoster, candidateData),
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"candidates": candidates})
}

type selectThumbnailRequest struct {
	Index *int `json:"index" binding:"required"`
}

// SelectThumbnail copies a candidate still into the movie's primary poster.
func (ic *ItemsController) SelectThumbnail(c *gin.Context) {
	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	var req selectThumbnailRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Index == nil || *req.Index < 0 || *req.Index > 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "index must be between 0 and 4"})
		return
	}

	sourcePath := filepath.Join(ic.dataDir, "images", "thumbnails", fmt.Sprintf("%d", movie.ID), fmt.Sprintf("%d.jpg", *req.Index))
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "thumbnail candidate not found"})
		return
	}
	destPath := filepath.Join(ic.dataDir, "images", "posters", fmt.Sprintf("%d.jpg", movie.ID))
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare poster directory"})
		return
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save poster"})
		return
	}
	movie.PosterPath = fmt.Sprintf("posters/%d.jpg", movie.ID)
	movie.PosterIsGenerated = true
	if err := ic.db.Save(&movie).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update movie"})
		return
	}
	c.JSON(http.StatusOK, movieToItem(movie))
}

type setSyncRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// SetDeviceSync marks or unmarks a movie as available to magicboxie-device
// Pis (see RegisterDevice) -- only movies with SyncEnabled set are ever
// offered to a device's opportunistic home-sync check-in.
func (ic *ItemsController) SetDeviceSync(c *gin.Context) {
	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}
	var req setSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled is required"})
		return
	}
	movie.SyncEnabled = *req.Enabled
	if err := ic.db.Save(&movie).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update movie"})
		return
	}
	c.JSON(http.StatusOK, movieToItem(movie))
}

type matchRequest struct {
	TMDBID int `json:"tmdb_id" binding:"required"`
}

// Match is a MagicBoxie-specific extension (no Jellyfin equivalent) letting
// the user manually correct an ambiguous or missing TMDB match.
func (ic *ItemsController) Match(c *gin.Context) {
	movie, ok := ic.loadMovie(c)
	if !ok {
		return
	}

	var req matchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tmdb_id is required"})
		return
	}

	if err := ic.importer.ApplyManualMatch(c.Request.Context(), &movie, req.TMDBID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, movieToItem(movie))
}

type tmdbSearchResult struct {
	TMDBID    int    `json:"tmdb_id"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	Overview  string `json:"overview,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
}

// Search is a MagicBoxie-specific extension letting the user look up TMDB
// candidates by title, to manually correct a movie whose automatic match was
// missing or wrong (see Match, which applies the chosen candidate).
func (ic *ItemsController) Search(c *gin.Context) {
	query := strings.TrimSpace(c.Query("query"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}
	year, _ := strconv.Atoi(c.Query("year"))

	results, err := ic.importer.SearchTMDB(c.Request.Context(), query, year)
	if err != nil {
		if errors.Is(err, tmdb.ErrNotConfigured) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "TMDB is not configured"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	out := make([]tmdbSearchResult, len(results))
	for i, r := range results {
		year := 0
		if len(r.ReleaseDate) >= 4 {
			year, _ = strconv.Atoi(r.ReleaseDate[:4])
		}
		posterURL := ""
		if r.PosterPath != "" {
			posterURL = "https://image.tmdb.org/t/p/w200" + r.PosterPath
		}
		out[i] = tmdbSearchResult{
			TMDBID:    r.ID,
			Title:     r.Title,
			Year:      year,
			Overview:  r.Overview,
			PosterURL: posterURL,
		}
	}
	c.JSON(http.StatusOK, gin.H{"results": out})
}

// Jobs exposes the durable transcode queue for the admin activity screen.
// Recent completed jobs are included so work does not disappear the instant
// it finishes, while the result remains small on long-running libraries.
func (ic *ItemsController) Jobs(c *gin.Context) {
	type jobRow struct {
		ID              uint       `json:"id"`
		MovieID         uint       `json:"movie_id"`
		MovieTitle      string     `json:"movie_title"`
		Type            string     `json:"type"`
		Status          string     `json:"status"`
		ProgressPercent float64    `json:"progress_percent"`
		LogTail         string     `json:"log_tail,omitempty"`
		StartedAt       *time.Time `json:"started_at,omitempty"`
		FinishedAt      *time.Time `json:"finished_at,omitempty"`
		CreatedAt       time.Time  `json:"created_at"`
	}

	var jobs []jobRow
	err := ic.db.Table("jobs").
		Select("jobs.id, jobs.movie_id, COALESCE(movies.title, 'Deleted movie') AS movie_title, jobs.type, jobs.status, jobs.progress_percent, jobs.log_tail, jobs.started_at, jobs.finished_at, jobs.created_at").
		Joins("LEFT JOIN movies ON movies.id = jobs.movie_id").
		Order("CASE jobs.status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END, jobs.created_at DESC").
		Limit(50).
		Scan(&jobs).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load background jobs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

// Scan is a MagicBoxie-specific extension that kicks off a movie library scan
// in the background and returns immediately.
func (ic *ItemsController) Scan(c *gin.Context) {
	go func() {
		n, err := ic.importer.Scan(context.Background())
		if err != nil {
			log.Printf("library scan: error: %v", err)
			return
		}
		log.Printf("library scan: imported %d new movie(s)", n)
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "scanning"})
}

// MusicScan is a MagicBoxie-specific extension that kicks off a music
// library scan in the background and returns immediately.
func (ic *ItemsController) MusicScan(c *gin.Context) {
	go func() {
		n, err := ic.musicImporter.Scan(context.Background())
		if err != nil {
			log.Printf("music library scan: error: %v", err)
			return
		}
		log.Printf("music library scan: imported %d new track(s)", n)
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "scanning"})
}

type registerDeviceRequest struct {
	DeviceID string `json:"device_id" binding:"required"`
}

// RegisterDevice is magicboxie-device's home_sync_service.py check-in: an
// unauthenticated, low-friction alternative to the Jellyfin login flow
// (device Pis have no interactive way to type a password each boot). It
// upserts the calling device's last-seen time for visibility, and returns
// only the movies explicitly marked SyncEnabled (see SetDeviceSync) and
// ready to play -- everything else in the library is left alone, since a
// device's local storage is finite and the point of "select videos" is
// the user choosing what a given Pi carries.
func (ic *ItemsController) RegisterDevice(c *gin.Context) {
	var req registerDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device_id is required"})
		return
	}

	device := models.Device{DeviceID: req.DeviceID, LastSeenAt: time.Now().UTC()}
	if err := ic.db.Where(models.Device{DeviceID: req.DeviceID}).Assign(device).FirstOrCreate(&device).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register device"})
		return
	}

	var movies []models.Movie
	if err := ic.db.Where("status = ? AND sync_enabled = ?", models.MovieStatusReady, true).
		Order("added_at desc").Find(&movies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list synced items"})
		return
	}
	items := make([]jellyfinItem, len(movies))
	for i, m := range movies {
		items[i] = movieToItem(m)
	}
	c.JSON(http.StatusOK, itemsResponse{Items: items, TotalRecordCount: len(items)})
}

// ListDevices reports every Pi that has ever called RegisterDevice and when
// it last did, for the web/app UI to show as connectivity - not an access
// control list (see Device's own doc comment).
func (ic *ItemsController) ListDevices(c *gin.Context) {
	var devices []models.Device
	if err := ic.db.Order("last_seen_at desc").Find(&devices).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list devices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

func (ic *ItemsController) loadMovie(c *gin.Context) (models.Movie, bool) {
	id, ok := parseItemID(c.Param("itemId"), "movie")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return models.Movie{}, false
	}
	var movie models.Movie
	if err := ic.db.First(&movie, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return models.Movie{}, false
	}
	return movie, true
}

func (ic *ItemsController) loadArtist(c *gin.Context) (models.Artist, bool) {
	id, ok := parseItemID(c.Param("itemId"), "artist")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return models.Artist{}, false
	}
	var artist models.Artist
	if err := ic.db.First(&artist, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return models.Artist{}, false
	}
	return artist, true
}

func (ic *ItemsController) loadAlbum(c *gin.Context) (models.Album, bool) {
	id, ok := parseItemID(c.Param("itemId"), "album")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return models.Album{}, false
	}
	var album models.Album
	if err := ic.db.First(&album, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return models.Album{}, false
	}
	return album, true
}

func (ic *ItemsController) loadTrack(c *gin.Context) (models.Track, bool) {
	id, ok := parseItemID(c.Param("itemId"), "track")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return models.Track{}, false
	}
	var track models.Track
	if err := ic.db.First(&track, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return models.Track{}, false
	}
	return track, true
}

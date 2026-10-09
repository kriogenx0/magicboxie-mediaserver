package routes_test

// Test harness for the Jellyfin API-conformance suite.
//
// The suite boots the real router (the same routes.Register + SPA fallback
// main.go wires up) against a temporary SQLite database seeded with a few
// movies and some music, then drives it over real HTTP the way a Jellyfin
// client would. Response bodies are validated against Jellyfin's own OpenAPI
// spec (testdata/jellyfin-openapi.json, refreshed by
// scripts/update-jellyfin-spec.sh), so "matches Jellyfin" is checked against
// Jellyfin's published contract rather than against our own assumptions.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"magicboxie/internal/auth"
	"magicboxie/internal/config"
	"magicboxie/internal/controllers"
	"magicboxie/internal/db"
	"magicboxie/internal/models"
	"magicboxie/internal/routes"
	"magicboxie/internal/services/events"
	"magicboxie/internal/services/library"
	"magicboxie/internal/services/music"
	"magicboxie/internal/services/tmdb"
	"magicboxie/internal/services/upload"
	"magicboxie/internal/web"
)

// Fixture ids (see seedLibrary). Item ids are "<kind>-<n>".
const (
	idToyStory  = "movie-1" // ready, has poster + backdrop + playable file
	idHeat      = "movie-2" // ready, no poster
	idAlien     = "movie-3" // ready, no poster
	idInception = "movie-4" // still processing: not playable yet
	idArtist    = "artist-1"
	idAlbum     = "album-1"
	idTrack1    = "track-1"
	idTrack2    = "track-2"

	testPassword = "correct horse battery staple"
)

// ---- Jellyfin OpenAPI spec ----

var (
	specOnce sync.Once
	specDoc  *openapi3.T
	specErr  error
)

func jellyfinSpec(t testing.TB) *openapi3.T {
	t.Helper()
	specOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join("testdata", "jellyfin-openapi.json"))
		if err != nil {
			specErr = err
			return
		}
		specDoc, specErr = openapi3.NewLoader().LoadFromData(data)
	})
	if specErr != nil {
		t.Fatalf("loading the vendored Jellyfin OpenAPI spec: %v", specErr)
	}
	return specDoc
}

type specOperation struct {
	Method string
	Path   string
	Op     *openapi3.Operation
}

func specOperations(t testing.TB) []specOperation {
	t.Helper()
	var ops []specOperation
	for path, item := range jellyfinSpec(t).Paths.Map() {
		for method, op := range item.Operations() {
			ops = append(ops, specOperation{Method: method, Path: path, Op: op})
		}
	}
	return ops
}

func specOperationByID(t testing.TB, operationID string) specOperation {
	t.Helper()
	for _, op := range specOperations(t) {
		if op.Op.OperationID == operationID {
			return op
		}
	}
	t.Fatalf("operation %q is not in the vendored Jellyfin spec", operationID)
	return specOperation{}
}

func responseSchema(t testing.TB, operationID string, status int) *openapi3.SchemaRef {
	t.Helper()
	ref := specOperationByID(t, operationID).Op.Responses.Status(status)
	if ref == nil || ref.Value == nil {
		t.Fatalf("spec has no %d response for %s", status, operationID)
	}
	media := ref.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		t.Fatalf("spec's %d response for %s has no application/json schema", status, operationID)
	}
	return media.Schema
}

func requestSchema(t testing.TB, operationID string) *openapi3.SchemaRef {
	t.Helper()
	body := specOperationByID(t, operationID).Op.RequestBody
	if body == nil || body.Value == nil {
		t.Fatalf("spec has no request body for %s", operationID)
	}
	media := body.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		t.Fatalf("spec's request body for %s has no application/json schema", operationID)
	}
	return media.Schema
}

// Item ids. The spec says BaseItemDto.Id is a UUID; ours are "<kind>-<n>"
// (e.g. "movie-5") plus the two fixed library-folder ids. This is the one
// place that deviation is tolerated -- see TestKnownDeviations, which fails
// the moment ids become real UUIDs so this allowance can't quietly rot.
var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)
	magicBoxieIDRe  = regexp.MustCompile(`^((movie|artist|album|track)-\d+|movies|music)$`)
	stringFormatFns = map[string]openapi3.StringFormatValidator{
		"uuid": openapi3.NewCallbackValidator(func(s string) error {
			if uuidPattern.MatchString(s) || magicBoxieIDRe.MatchString(s) {
				return nil
			}
			return fmt.Errorf("%q is neither a UUID nor a MagicBoxie item id", s)
		}),
		"date-time": openapi3.NewCallbackValidator(func(s string) error {
			_, err := time.Parse(time.RFC3339Nano, s)
			return err
		}),
	}
)

// stripExtensions removes MagicBoxie's additive extension fields (all
// "MagicBoxie"-prefixed) so the rest of each object can be validated
// strictly: Jellyfin's schemas forbid additional properties, so a misspelled
// or wrongly-cased standard field is still caught, while the intentional,
// namespaced extensions (which real clients ignore) are not.
func stripExtensions(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k := range val {
			if strings.HasPrefix(k, "MagicBoxie") {
				delete(val, k)
				continue
			}
			val[k] = stripExtensions(val[k])
		}
		return val
	case []any:
		for i := range val {
			val[i] = stripExtensions(val[i])
		}
		return val
	}
	return v
}

// schemaProblems validates body against schema and returns one line per
// violation ("/Items/0/Id: ..."), or nil when it conforms.
func schemaProblems(schema *openapi3.SchemaRef, body []byte) []string {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return []string{fmt.Sprintf("body is not valid JSON: %v (body: %.200s)", err, body)}
	}
	err := schema.Value.VisitJSON(stripExtensions(doc),
		openapi3.MultiErrors(),
		openapi3.VisitAsResponse(),
		openapi3.EnableFormatValidation(),
		openapi3.WithStringFormatValidators(stringFormatFns),
	)
	if err == nil {
		return nil
	}
	return flattenSchemaErrors(err)
}

func flattenSchemaErrors(err error) []string {
	var multi openapi3.MultiError
	if errors.As(err, &multi) {
		var out []string
		for _, e := range multi {
			out = append(out, flattenSchemaErrors(e)...)
		}
		return out
	}
	var se *openapi3.SchemaError
	if errors.As(err, &se) {
		return []string{fmt.Sprintf("/%s: %s", strings.Join(se.JSONPointer(), "/"), se.Reason)}
	}
	return []string{err.Error()}
}

// ---- server under test ----

type testServer struct {
	t         *testing.T
	router    *gin.Engine
	http      *httptest.Server
	client    *http.Client
	token     string
	userID    string
	movieFile []byte
	trackFile []byte
	poster    []byte
}

// newTestServer builds the same router main.go serves (Jellyfin surface +
// SPA fallback) over a fresh, seeded temp library and logs in once.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	cfg := &config.Config{
		MoviesDir: filepath.Join(root, "movies"),
		MusicDir:  filepath.Join(root, "music"),
		DataDir:   filepath.Join(root, "data"),
	}
	for _, dir := range []string{cfg.MoviesDir, cfg.MusicDir, cfg.DataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.PasswordHash = hash

	gormDB, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gormDB.DB(); err == nil {
			sqlDB.Close()
		}
	})
	authManager, err := auth.NewManager(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}

	importer := library.NewImporter(gormDB, cfg.MoviesDir, cfg.DataDir, tmdb.NewClient(""))
	musicImporter := music.NewImporter(gormDB, cfg.MusicDir, cfg.DataDir)
	hub := events.NewHub()
	uploadManager, err := upload.NewManager(gormDB, cfg.MoviesDir)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	routes.Register(router, routes.Dependencies{
		AuthManager:       authManager,
		AuthController:    controllers.NewAuthController(cfg, authManager),
		ItemsController:   controllers.NewItemsController(gormDB, importer, musicImporter, cfg.MoviesDir, cfg.DataDir),
		VideosController:  controllers.NewVideosController(gormDB, cfg.MoviesDir, cfg.DataDir),
		AudioController:   controllers.NewAudioController(gormDB, cfg.MusicDir),
		UploadsController: controllers.NewUploadsController(uploadManager, cfg.MoviesDir, cfg.MusicDir, importer, musicImporter),
		EventsHub:         hub,
	})
	// A stand-in SPA bundle, so unrouted paths behave as they do in
	// production: API-shaped ones must 404 as JSON, never fall through to
	// index.html (which generated clients then fail to decode).
	web.RegisterSPA(router, http.FS(fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>spa</title>")}}))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	s := &testServer{
		t:      t,
		router: router,
		http:   srv,
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	s.seedLibrary(gormDB, cfg)

	login := s.post("/Users/AuthenticateByName", map[string]string{"Username": "admin", "Pw": testPassword}, noAuth)
	if login.Status != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Status, login.Body)
	}
	var result struct {
		AccessToken string
		User        struct{ Id string }
	}
	login.decode(t, &result)
	s.token, s.userID = result.AccessToken, result.User.Id
	return s
}

func (s *testServer) seedLibrary(gormDB *gorm.DB, cfg *config.Config) {
	t := s.t
	t.Helper()

	var jpg bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	for x := 0; x < 4; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(40 * y), B: 90, A: 255})
		}
	}
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	s.poster = jpg.Bytes()
	s.movieFile = bytes.Repeat([]byte("0123456789abcdef"), 64) // 1 KiB
	s.trackFile = bytes.Repeat([]byte("fedcba9876543210"), 32)

	write := func(path string, data []byte) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg.DataDir, "images", "posters", "1.jpg"), s.poster)
	write(filepath.Join(cfg.DataDir, "images", "backdrops", "1.jpg"), s.poster)
	write(filepath.Join(cfg.MoviesDir, "toy_story.mp4"), s.movieFile)
	write(filepath.Join(cfg.DataDir, "player", "1.mp4"), s.movieFile[:512])
	write(filepath.Join(cfg.MusicDir, "kob", "01 So What.flac"), s.trackFile)
	write(filepath.Join(cfg.MusicDir, "kob", "02 Freddie Freeloader.flac"), s.trackFile)

	cast, err := json.Marshal([]tmdb.CastMember{{Name: "Tom Hanks", Character: "Woody"}, {Name: "Tim Allen", Character: "Buzz Lightyear"}})
	if err != nil {
		t.Fatal(err)
	}
	tmdbID := 862
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	movies := []models.Movie{
		{ID: 1, Title: "Toy Story", Year: 1995, Overview: "A cowboy doll is profoundly threatened.", TMDBID: &tmdbID,
			GenresJSON: `["Animation","Family","Comedy"]`, CastJSON: string(cast),
			PosterPath: "posters/1.jpg", BackdropPath: "backdrops/1.jpg",
			OriginalFilename: "toy_story.mkv", SourceRelpath: "toy_story.mkv", PlayableRelpath: "toy_story.mp4",
			FileSizeBytes: int64(len(s.movieFile)), DurationSeconds: 4860, VideoCodec: "h264", AudioCodec: "aac", Container: "mp4",
			Status: models.MovieStatusReady, PlayerStatus: models.PlayerStatusReady, AddedAt: base.Add(1 * time.Hour)},
		{ID: 2, Title: "Heat", Year: 1995, GenresJSON: `["Crime","Drama","Thriller"]`,
			OriginalFilename: "heat.mp4", SourceRelpath: "heat.mp4", PlayableRelpath: "heat.mp4",
			DurationSeconds: 10200, VideoCodec: "h264", AudioCodec: "aac", Container: "mp4",
			Status: models.MovieStatusReady, AddedAt: base.Add(3 * time.Hour)},
		{ID: 3, Title: "The Alien", Year: 1979, GenresJSON: `["Horror","Science Fiction"]`,
			OriginalFilename: "alien.mp4", SourceRelpath: "alien.mp4", PlayableRelpath: "alien.mp4",
			DurationSeconds: 7020, VideoCodec: "h264", AudioCodec: "aac", Container: "mp4",
			Status: models.MovieStatusReady, AddedAt: base.Add(2 * time.Hour)},
		{ID: 4, Title: "Inception", Year: 2010,
			OriginalFilename: "inception.mkv", SourceRelpath: "inception.mkv",
			Status: models.MovieStatusTranscoding, AddedAt: base.Add(4 * time.Hour)},
	}
	if err := gormDB.Create(&movies).Error; err != nil {
		t.Fatal(err)
	}

	if err := gormDB.Create(&models.Artist{ID: 1, Name: "Miles Davis"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := gormDB.Create(&models.Album{ID: 1, ArtistID: 1, Title: "Kind of Blue", Year: 1959}).Error; err != nil {
		t.Fatal(err)
	}
	tracks := []models.Track{
		{ID: 1, AlbumID: 1, Title: "So What", TrackNumber: 1, DiscNumber: 1, DurationSeconds: 562, FileRelpath: "kob/01 So What.flac", Codec: "flac"},
		{ID: 2, AlbumID: 1, Title: "Freddie Freeloader", TrackNumber: 2, DiscNumber: 1, DurationSeconds: 590, FileRelpath: "kob/02 Freddie Freeloader.flac", Codec: "flac"},
	}
	if err := gormDB.Create(&tracks).Error; err != nil {
		t.Fatal(err)
	}
}

// ---- HTTP helpers ----

type response struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r *response) decode(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decoding response (status %d): %v\nbody: %.300s", r.Status, err, r.Body)
	}
}

type requestOption func(*http.Request)

// noAuth drops the default credentials, for anonymous endpoints and for
// exercising the other token transports explicitly.
func noAuth(r *http.Request) { r.Header.Del("Authorization") }

func withHeader(key, value string) requestOption {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

// mediaBrowser formats the header value Jellyfin clients send, e.g. Swiftfin:
//
//	MediaBrowser Client="Swiftfin", Device="Apple TV", DeviceId="...", Version="1.0.0", Token="..."
func mediaBrowser(token string) string {
	v := `MediaBrowser Client="Swiftfin", Device="Apple TV", DeviceId="conformance-test", Version="1.0.0"`
	if token != "" {
		v += fmt.Sprintf(`, Token=%q`, token)
	}
	return v
}

func (s *testServer) do(method, target string, body any, opts ...requestOption) *response {
	s.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, s.http.URL+target, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", mediaBrowser(s.token))
	for _, opt := range opts {
		opt(req)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return &response{Status: resp.StatusCode, Header: resp.Header, Body: data}
}

func (s *testServer) get(target string, opts ...requestOption) *response {
	s.t.Helper()
	return s.do(http.MethodGet, target, nil, opts...)
}

func (s *testServer) post(target string, body any, opts ...requestOption) *response {
	s.t.Helper()
	return s.do(http.MethodPost, target, body, opts...)
}

// requireOperation asserts r has the given status and, for JSON operations,
// that its body conforms to the spec's response schema for operationID.
func requireOperation(t *testing.T, operationID string, status int, r *response) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: status = %d, want %d\nbody: %.400s", operationID, r.Status, status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s: Content-Type = %q, want application/json", operationID, ct)
	}
	if problems := schemaProblems(responseSchema(t, operationID, status), r.Body); len(problems) > 0 {
		t.Errorf("%s: response does not match Jellyfin's schema:\n  %s\nbody: %.600s",
			operationID, strings.Join(problems, "\n  "), r.Body)
	}
}

// itemsPage is the subset of BaseItemDtoQueryResult the behavioural tests read.
type itemsPage struct {
	Items            []itemSummary
	TotalRecordCount int
	StartIndex       int
}

type itemSummary struct {
	Id             string
	Name           string
	Type           string
	CollectionType string
	MediaType      string
	SortName       string
	Genres         []string
}

func (p itemsPage) names() []string {
	out := make([]string, len(p.Items))
	for i, item := range p.Items {
		out[i] = item.Name
	}
	return out
}

func (s *testServer) items(t *testing.T, target string) itemsPage {
	t.Helper()
	r := s.get(target)
	requireOperation(t, "GetItems", http.StatusOK, r)
	var page itemsPage
	r.decode(t, &page)
	return page
}

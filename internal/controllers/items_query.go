package controllers

import (
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// kidsGenres are the TMDB genres that make a title "kids": TMDB has no single
// kids genre, so Animation and Family stand in (the web UI's Kids row uses the
// same pair). Backs the isKids filter Swiftfin's Kids category sends.
var kidsGenres = map[string]bool{"animation": true, "family": true}

// ---- query-string helpers ----
//
// ASP.NET binds query keys case-insensitively, and real clients differ:
// Swiftfin's SDK sends includeItemTypes/parentId/sortBy, older clients and this
// repo's own web UI send IncludeItemTypes/ParentId. Array parameters arrive
// either as repeated keys or comma-separated.

func queryValues(c *gin.Context, name string) []string {
	var out []string
	for key, values := range c.Request.URL.Query() {
		if strings.EqualFold(key, name) {
			out = append(out, values...)
		}
	}
	return out
}

func queryList(c *gin.Context, name string) []string {
	var out []string
	for _, value := range queryValues(c, name) {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func queryString(c *gin.Context, name string) string {
	if values := queryValues(c, name); len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

func queryInt(c *gin.Context, name string) (int, bool) {
	n, err := strconv.Atoi(queryString(c, name))
	return n, err == nil
}

func queryBool(c *gin.Context, name string) bool {
	return strings.EqualFold(queryString(c, name), "true")
}

func lowerSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[strings.ToLower(v)] = true
	}
	return set
}

// ---- the parsed GET /Items request ----

type itemsQuery struct {
	kinds          map[string]bool // lower-cased IncludeItemTypes
	excludeKinds   map[string]bool
	mediaTypes     map[string]bool
	parentID       string
	ids            map[string]bool
	searchTerm     string
	nameStartsWith string
	nameLessThan   string
	genres         map[string]bool
	years          map[int]bool
	kidsOnly       bool
	moviesOnly     bool
	needsUserData  bool // favourites/played/resumable: per-user state we don't track
	sortBy         []string
	sortOrder      []string
	startIndex     int
	limit          int // negative: unlimited
	includeUnready bool
}

// parseItemsQuery reads the parameters Jellyfin clients send to list items.
// includeUnready keeps movies that aren't playable yet (still being probed or
// transcoded): the legacy path wants them, because the web UI shows them under
// "Continue Processing", but Jellyfin's own /Items must not offer what can't play.
func parseItemsQuery(c *gin.Context, includeUnready bool) itemsQuery {
	q := itemsQuery{
		kinds:          lowerSet(queryList(c, "includeItemTypes")),
		excludeKinds:   lowerSet(queryList(c, "excludeItemTypes")),
		mediaTypes:     lowerSet(queryList(c, "mediaTypes")),
		parentID:       queryString(c, "parentId"),
		ids:            map[string]bool{},
		searchTerm:     strings.ToLower(queryString(c, "searchTerm")),
		nameStartsWith: strings.ToLower(queryString(c, "nameStartsWith")),
		nameLessThan:   strings.ToLower(queryString(c, "nameLessThan")),
		genres:         map[string]bool{},
		years:          map[int]bool{},
		kidsOnly:       queryBool(c, "isKids"),
		moviesOnly:     queryBool(c, "isMovie"),
		sortBy:         queryList(c, "sortBy"),
		sortOrder:      queryList(c, "sortOrder"),
		limit:          -1,
		includeUnready: includeUnready,
	}
	for _, id := range queryList(c, "ids") {
		q.ids[id] = true
	}
	// Jellyfin documents genres as pipe-delimited; SDKs may send commas.
	for _, raw := range queryValues(c, "genres") {
		for _, genre := range strings.FieldsFunc(raw, func(r rune) bool { return r == '|' || r == ',' }) {
			q.genres[strings.ToLower(strings.TrimSpace(genre))] = true
		}
	}
	for _, y := range queryList(c, "years") {
		if year, err := strconv.Atoi(y); err == nil {
			q.years[year] = true
		}
	}
	for _, f := range queryList(c, "filters") {
		switch strings.ToLower(f) {
		case "isfavorite", "isplayed", "isresumable", "likes", "dislikes":
			q.needsUserData = true
		}
	}
	if queryBool(c, "isFavorite") || queryBool(c, "isPlayed") {
		q.needsUserData = true
	}
	if n, ok := queryInt(c, "startIndex"); ok && n > 0 {
		q.startIndex = n
	}
	if n, ok := queryInt(c, "limit"); ok && n >= 0 {
		q.limit = n
	}
	return q
}

// itemScope is which kinds of item a request is asking about.
type itemScope struct {
	movies, artists, albums, tracks bool
	artistID, albumID               uint // non-zero: restrict to that parent
}

// scope resolves ParentId and IncludeItemTypes into the item kinds to load.
// MagicBoxie has two fixed libraries ("movies" and "music") plus the
// artist -> album -> track hierarchy inside music.
func (q itemsQuery) scope() itemScope {
	wants := func(kind string) bool { return len(q.kinds) == 0 || q.kinds[kind] }
	var sc itemScope

	switch {
	case q.parentID == "":
		if len(q.kinds) == 0 {
			// An unqualified listing has always meant the movie library.
			sc.movies = true
			return sc
		}
		sc = itemScope{movies: q.kinds["movie"], artists: q.kinds["musicartist"], albums: q.kinds["musicalbum"], tracks: q.kinds["audio"]}
	case q.parentID == "movies":
		sc.movies = wants("movie")
	case q.parentID == "music":
		if len(q.kinds) == 0 {
			sc.artists = true // a music library's top level is its artists
		} else {
			sc.artists, sc.albums, sc.tracks = q.kinds["musicartist"], q.kinds["musicalbum"], q.kinds["audio"]
		}
	default:
		if id, ok := parseItemID(q.parentID, "artist"); ok {
			sc.artistID = id
			sc.albums = wants("musicalbum")
			sc.tracks = q.kinds["audio"]
		} else if id, ok := parseItemID(q.parentID, "album"); ok {
			sc.albumID = id
			sc.tracks = wants("audio")
		}
		// Any other parent (a movie, an unknown id) has no children.
	}
	return sc
}

// filter applies the request's filters to items.
func (q itemsQuery) filter(items []jellyfinItem) []jellyfinItem {
	if q.needsUserData {
		return nil // nothing is ever favourited, played or resumable
	}
	out := items[:0:0]
	for _, item := range items {
		if len(q.ids) > 0 && !q.ids[item.Id] {
			continue
		}
		if q.excludeKinds[strings.ToLower(item.Type)] {
			continue
		}
		if len(q.mediaTypes) > 0 && !q.mediaTypes[strings.ToLower(item.MediaType)] {
			continue
		}
		if q.moviesOnly && item.Type != "Movie" {
			continue
		}
		if q.searchTerm != "" &&
			!strings.Contains(strings.ToLower(item.Name), q.searchTerm) &&
			!strings.Contains(strings.ToLower(item.AlbumArtist), q.searchTerm) {
			continue
		}
		if q.nameStartsWith != "" && !strings.HasPrefix(item.SortName, q.nameStartsWith) {
			continue
		}
		if q.nameLessThan != "" && item.SortName >= q.nameLessThan {
			continue
		}
		if len(q.years) > 0 && !q.years[item.ProductionYear] {
			continue
		}
		if len(q.genres) > 0 && !hasGenre(item, q.genres) {
			continue
		}
		if q.kidsOnly && !hasGenre(item, kidsGenres) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func hasGenre(item jellyfinItem, wanted map[string]bool) bool {
	for _, genre := range item.Genres {
		if wanted[strings.ToLower(genre)] {
			return true
		}
	}
	return false
}

// sort orders items by the request's sortBy keys (each with its own sortOrder,
// ascending when unspecified). With no sortBy the natural order of each kind
// is kept: newest movies first, artists/albums by name, tracks by disc/track.
func (q itemsQuery) sort(items []jellyfinItem) {
	if len(q.sortBy) == 0 {
		return
	}
	for _, key := range q.sortBy {
		if strings.EqualFold(key, "random") {
			rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
			return
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		for n, key := range q.sortBy {
			cmp := compareItems(items[i], items[j], strings.ToLower(key))
			if cmp == 0 {
				continue
			}
			if n < len(q.sortOrder) && strings.EqualFold(q.sortOrder[n], "descending") {
				cmp = -cmp
			}
			return cmp < 0
		}
		return false
	})
}

func compareItems(a, b jellyfinItem, key string) int {
	switch key {
	case "name", "sortname":
		return strings.Compare(a.SortName, b.SortName)
	case "datecreated", "datelastcontentadded":
		return strings.Compare(a.DateCreated, b.DateCreated)
	case "productionyear", "premieredate":
		return a.ProductionYear - b.ProductionYear
	case "runtime":
		return compareInt64(a.RunTimeTicks, b.RunTimeTicks)
	case "indexnumber":
		return a.IndexNumber - b.IndexNumber
	case "parentindexnumber":
		return a.ParentIndexNumber - b.ParentIndexNumber
	case "album":
		return strings.Compare(strings.ToLower(a.Album), strings.ToLower(b.Album))
	case "albumartist", "artist":
		return strings.Compare(strings.ToLower(a.AlbumArtist), strings.ToLower(b.AlbumArtist))
	}
	return 0 // keys with no data behind them (ratings, play counts...) don't reorder
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// page returns the requested window of items (never nil, so it encodes as []).
func (q itemsQuery) page(items []jellyfinItem) []jellyfinItem {
	if q.startIndex >= len(items) {
		return []jellyfinItem{}
	}
	items = items[q.startIndex:]
	if q.limit >= 0 && q.limit < len(items) {
		items = items[:q.limit]
	}
	return items
}

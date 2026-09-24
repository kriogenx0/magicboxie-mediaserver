package routes_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// Each test walks the requests a real Jellyfin client makes for one part of a
// session, using the exact query strings the clients send (Swiftfin's are
// taken from its SDK: camelCase keys, comma-separated lists), and validates
// every response against Jellyfin's OpenAPI schema.

func TestDiscoveryAndLogin(t *testing.T) {
	s := newTestServer(t)

	t.Run("public system info", func(t *testing.T) {
		r := s.get("/System/Info/Public", noAuth)
		requireOperation(t, "GetPublicSystemInfo", http.StatusOK, r)
		var info struct{ ServerName, Id string }
		r.decode(t, &info)
		if info.ServerName == "" || info.Id == "" {
			t.Errorf("ServerName and Id must be set (clients key the saved server on Id): %+v", info)
		}
	})

	t.Run("login screen", func(t *testing.T) {
		requireOperation(t, "GetPublicUsers", http.StatusOK, s.get("/Users/Public", noAuth))
		requireOperation(t, "GetQuickConnectEnabled", http.StatusOK, s.get("/QuickConnect/Enabled", noAuth))
		requireOperation(t, "GetBrandingOptions", http.StatusOK, s.get("/Branding/Configuration", noAuth))
	})

	t.Run("authenticate", func(t *testing.T) {
		body := map[string]string{"Username": "admin", "Pw": testPassword}
		// Clients identify themselves via the (X-Emby-)Authorization header,
		// with no token yet.
		for _, header := range []string{"Authorization", "X-Emby-Authorization"} {
			r := s.post("/Users/AuthenticateByName", body, noAuth, withHeader(header, mediaBrowser("")))
			requireOperation(t, "AuthenticateUserByName", http.StatusOK, r)
			var result struct {
				AccessToken string
				ServerId    string
				User        struct{ Id, Name string }
			}
			r.decode(t, &result)
			if result.AccessToken == "" || result.User.Id == "" || result.ServerId == "" {
				t.Errorf("(%s) AuthenticationResult is missing AccessToken/ServerId/User.Id: %+v", header, result)
			}
		}
	})

	t.Run("wrong password is rejected", func(t *testing.T) {
		r := s.post("/Users/AuthenticateByName", map[string]string{"Username": "admin", "Pw": "nope"}, noAuth)
		if r.Status != http.StatusUnauthorized {
			t.Errorf("wrong password: status = %d, want 401", r.Status)
		}
	})

	t.Run("malformed login body is rejected", func(t *testing.T) {
		r := s.do(http.MethodPost, "/Users/AuthenticateByName", nil, noAuth)
		if r.Status != http.StatusBadRequest {
			t.Errorf("empty body: status = %d, want 400", r.Status)
		}
	})

	t.Run("signed-in basics", func(t *testing.T) {
		me := s.get("/Users/Me")
		requireOperation(t, "GetCurrentUser", http.StatusOK, me)
		var user struct{ Id string }
		me.decode(t, &user)
		requireOperation(t, "GetUserById", http.StatusOK, s.get("/Users/"+user.Id))

		requireOperation(t, "GetSystemInfo", http.StatusOK, s.get("/System/Info"))

		ping := s.get("/System/Ping")
		requireOperation(t, "GetPingSystem", http.StatusOK, ping)
		if got := strings.TrimSpace(string(ping.Body)); got != `"Jellyfin Server"` {
			t.Errorf("ping body = %s, want \"Jellyfin Server\"", got)
		}
	})
}

func TestAuthTransports(t *testing.T) {
	s := newTestServer(t)
	tok := s.token

	// Every way Jellyfin's server accepts an access token, plus the Bearer
	// header MagicBoxie's own web UI uses.
	transports := []struct {
		name  string
		opt   requestOption
		query string
	}{
		{"Authorization: MediaBrowser Token", func(r *http.Request) { r.Header.Set("Authorization", mediaBrowser(tok)) }, ""},
		{"X-Emby-Authorization: MediaBrowser Token", func(r *http.Request) { noAuth(r); r.Header.Set("X-Emby-Authorization", mediaBrowser(tok)) }, ""},
		{"X-Emby-Token", func(r *http.Request) { noAuth(r); r.Header.Set("X-Emby-Token", tok) }, ""},
		{"X-MediaBrowser-Token", func(r *http.Request) { noAuth(r); r.Header.Set("X-MediaBrowser-Token", tok) }, ""},
		{"api_key query", noAuth, "?api_key=" + tok},
		{"ApiKey query", noAuth, "?ApiKey=" + tok},
		{"Authorization: Bearer (web UI)", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }, ""},
	}
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			if r := s.get("/Users/Me"+tr.query, tr.opt); r.Status != http.StatusOK {
				t.Errorf("status = %d, want 200 (token sent via %s)\nbody: %s", r.Status, tr.name, r.Body)
			}
		})
	}

	t.Run("no token", func(t *testing.T) {
		if r := s.get("/Users/Me", noAuth); r.Status != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", r.Status)
		}
	})
	t.Run("invalid token", func(t *testing.T) {
		if r := s.get("/Users/Me", withHeader("Authorization", mediaBrowser("not-a-real-token"))); r.Status != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", r.Status)
		}
	})
	t.Run("every non-public endpoint requires a token", func(t *testing.T) {
		for _, target := range []string{
			"/Items", "/Items/Latest", "/Items/" + idToyStory, "/UserItems/Resume", "/Shows/NextUp", "/UserViews",
			"/System/Info", "/Items/" + idToyStory + "/PlaybackInfo", "/Videos/" + idToyStory + "/stream",
		} {
			if r := s.get(target, noAuth); r.Status != http.StatusUnauthorized {
				t.Errorf("GET %s without a token = %d, want 401", target, r.Status)
			}
		}
	})
}

func TestHomeScreen(t *testing.T) {
	s := newTestServer(t)
	uid := s.userID

	t.Run("user views carry a CollectionType", func(t *testing.T) {
		r := s.get("/UserViews?userId=" + uid)
		requireOperation(t, "GetUserViews", http.StatusOK, r)
		var page itemsPage
		r.decode(t, &page)
		got := map[string]string{}
		for _, item := range page.Items {
			if item.Type != "CollectionFolder" {
				t.Errorf("view %q has Type %q, want CollectionFolder", item.Name, item.Type)
			}
			got[item.Name] = item.CollectionType
		}
		// Swiftfin builds a "Latest in <library>" home row only for views whose
		// CollectionType is movies/tvshows/musicvideos/homevideos.
		want := map[string]string{"Movies": "movies", "Music": "music"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("views = %v, want %v", got, want)
		}
	})

	t.Run("latest items", func(t *testing.T) {
		r := s.get("/Items/Latest?userId=" + uid + "&parentId=movies&limit=2&fields=PrimaryImageAspectRatio&imageTypeLimit=1&enableImageTypes=Primary")
		requireOperation(t, "GetLatestMedia", http.StatusOK, r)
		var items []itemSummary
		r.decode(t, &items)
		// Newest first, playable only (Inception is still transcoding), capped by limit.
		if got, want := (itemsPage{Items: items}).names(), []string{"Heat", "The Alien"}; !reflect.DeepEqual(got, want) {
			t.Errorf("latest = %v, want %v", got, want)
		}
	})

	t.Run("resume and next-up rows are valid and empty", func(t *testing.T) {
		for op, target := range map[string]string{
			"GetResumeItems": "/UserItems/Resume?userId=" + uid + "&mediaTypes=Video&limit=12",
			"GetNextUp":      "/Shows/NextUp?userId=" + uid + "&limit=12",
		} {
			r := s.get(target)
			requireOperation(t, op, http.StatusOK, r)
			var page itemsPage
			r.decode(t, &page)
			if len(page.Items) != 0 || page.TotalRecordCount != 0 {
				t.Errorf("%s: want an empty page (no watch history is tracked), got %+v", op, page)
			}
		}
	})
}

func TestLibraryBrowse(t *testing.T) {
	s := newTestServer(t)
	uid := s.userID

	t.Run("opening the Movies library", func(t *testing.T) {
		// Swiftfin's request for a library whose parent is the "movies" view:
		// every supported item type, recursive, sorted by name.
		page := s.items(t, "/Items?userId="+uid+"&parentId=movies"+
			"&includeItemTypes=Movie,Series,Episode,BoxSet,Video,MusicVideo"+
			"&recursive=true&sortBy=SortName&sortOrder=Ascending&startIndex=0&limit=50&enableUserData=true")
		if got, want := page.names(), []string{"The Alien", "Heat", "Toy Story"}; !reflect.DeepEqual(got, want) {
			t.Errorf("movies = %v, want %v (playable only; alphabetical ignoring a leading \"The\")", got, want)
		}
		if page.TotalRecordCount != 3 {
			t.Errorf("TotalRecordCount = %d, want 3", page.TotalRecordCount)
		}
		for _, item := range page.Items {
			if item.Type != "Movie" || item.MediaType != "Video" {
				t.Errorf("%s: Type=%q MediaType=%q, want Movie/Video", item.Name, item.Type, item.MediaType)
			}
		}
	})

	t.Run("tvOS Movies tab has no parentId", func(t *testing.T) {
		page := s.items(t, "/Items?userId="+uid+"&includeItemTypes=Movie&recursive=true&sortBy=SortName&sortOrder=Ascending&limit=50&startIndex=0")
		if got, want := page.names(), []string{"The Alien", "Heat", "Toy Story"}; !reflect.DeepEqual(got, want) {
			t.Errorf("movies = %v, want %v", got, want)
		}
	})

	t.Run("paging", func(t *testing.T) {
		page := s.items(t, "/Items?includeItemTypes=Movie&recursive=true&sortBy=SortName&sortOrder=Ascending&limit=2&startIndex=1")
		if got, want := page.names(), []string{"Heat", "Toy Story"}; !reflect.DeepEqual(got, want) {
			t.Errorf("page = %v, want %v", got, want)
		}
		if page.TotalRecordCount != 3 || page.StartIndex != 1 {
			t.Errorf("TotalRecordCount=%d StartIndex=%d, want 3 and 1 (the total is the full match count, not the page size)", page.TotalRecordCount, page.StartIndex)
		}
		past := s.items(t, "/Items?includeItemTypes=Movie&recursive=true&limit=2&startIndex=3")
		if len(past.Items) != 0 || past.TotalRecordCount != 3 {
			t.Errorf("paging past the end = %+v, want no items and TotalRecordCount 3", past)
		}
		none := s.items(t, "/Items?includeItemTypes=Movie&recursive=true&limit=0")
		if len(none.Items) != 0 {
			t.Errorf("limit=0 returned %d items, want 0", len(none.Items))
		}
	})

	t.Run("sorting", func(t *testing.T) {
		desc := s.items(t, "/Items?includeItemTypes=Movie&sortBy=SortName&sortOrder=Descending")
		if got, want := desc.names(), []string{"Toy Story", "Heat", "The Alien"}; !reflect.DeepEqual(got, want) {
			t.Errorf("SortName desc = %v, want %v", got, want)
		}
		added := s.items(t, "/Items?includeItemTypes=Movie&sortBy=DateCreated&sortOrder=Descending")
		if got, want := added.names(), []string{"Heat", "The Alien", "Toy Story"}; !reflect.DeepEqual(got, want) {
			t.Errorf("DateCreated desc = %v, want %v", got, want)
		}
		year := s.items(t, "/Items?includeItemTypes=Movie&sortBy=ProductionYear,SortName&sortOrder=Ascending")
		if got, want := year.names(), []string{"The Alien", "Heat", "Toy Story"}; !reflect.DeepEqual(got, want) {
			t.Errorf("ProductionYear,SortName = %v, want %v", got, want)
		}
	})

	t.Run("query parameter names are case-insensitive", func(t *testing.T) {
		// ASP.NET binds query keys case-insensitively, so older clients that
		// send PascalCase keys must work exactly like camelCase ones.
		page := s.items(t, "/Items?IncludeItemTypes=Movie&SortBy=SortName&Limit=1&StartIndex=1")
		if got, want := page.names(), []string{"Heat"}; !reflect.DeepEqual(got, want) {
			t.Errorf("PascalCase params = %v, want %v", got, want)
		}
	})

	t.Run("filters", func(t *testing.T) {
		cases := []struct {
			name, query string
			want        []string
		}{
			{"searchTerm", "searchTerm=STORY", []string{"Toy Story"}},
			{"nameStartsWith", "nameStartsWith=h", []string{"Heat"}},
			{"genres (pipe-delimited)", "genres=Crime|Horror", []string{"The Alien", "Heat"}},
			{"genres (comma-delimited)", "genres=Animation,Crime", []string{"Heat", "Toy Story"}},
			{"years", "years=1995", []string{"Heat", "Toy Story"}},
			{"ids", "ids=" + idHeat + "," + idAlien, []string{"The Alien", "Heat"}},
			{"isKids", "isKids=true", []string{"Toy Story"}},
			{"isMovie", "isMovie=true", []string{"The Alien", "Heat", "Toy Story"}},
			{"favorites (none tracked)", "filters=IsFavorite", nil},
			{"played (none tracked)", "filters=IsPlayed", nil},
			{"unplayed matches everything", "filters=IsUnplayed", []string{"The Alien", "Heat", "Toy Story"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				page := s.items(t, "/Items?includeItemTypes=Movie&recursive=true&sortBy=SortName&"+tc.query)
				if got := page.names(); !reflect.DeepEqual(got, orEmpty(tc.want)) {
					t.Errorf("%s: got %v, want %v", tc.query, got, tc.want)
				}
			})
		}
	})

	t.Run("unrecognised parameters are ignored", func(t *testing.T) {
		page := s.items(t, "/Items?includeItemTypes=Movie&fields=Overview,PrimaryImageAspectRatio&enableImageTypes=Primary,Backdrop,Thumb&imageTypeLimit=1&someFutureParam=1")
		if len(page.Items) != 3 {
			t.Errorf("got %d items, want 3", len(page.Items))
		}
	})

	t.Run("music hierarchy", func(t *testing.T) {
		artists := s.items(t, "/Items?includeItemTypes=MusicArtist&recursive=true&sortBy=SortName")
		if got, want := artists.names(), []string{"Miles Davis"}; !reflect.DeepEqual(got, want) || artists.Items[0].Type != "MusicArtist" {
			t.Errorf("artists = %+v, want Miles Davis (MusicArtist)", artists.Items)
		}
		albums := s.items(t, "/Items?includeItemTypes=MusicAlbum&parentId="+idArtist)
		if got, want := albums.names(), []string{"Kind of Blue"}; !reflect.DeepEqual(got, want) || albums.Items[0].Type != "MusicAlbum" {
			t.Errorf("albums = %+v, want Kind of Blue (MusicAlbum)", albums.Items)
		}
		tracks := s.items(t, "/Items?includeItemTypes=Audio&parentId="+idAlbum+"&sortBy=ParentIndexNumber,IndexNumber,SortName")
		if got, want := tracks.names(), []string{"So What", "Freddie Freeloader"}; !reflect.DeepEqual(got, want) {
			t.Errorf("tracks = %v, want %v (disc/track order)", got, want)
		}
		if tracks.Items[0].Type != "Audio" || tracks.Items[0].MediaType != "Audio" {
			t.Errorf("track Type/MediaType = %q/%q, want Audio/Audio", tracks.Items[0].Type, tracks.Items[0].MediaType)
		}
	})

	t.Run("libraries are separate", func(t *testing.T) {
		// Movie-library queries never surface music, and vice versa.
		if page := s.items(t, "/Items?parentId=movies&recursive=true"); len(page.Items) != 3 {
			t.Errorf("parentId=movies returned %v, want just the 3 playable movies", page.names())
		}
		if page := s.items(t, "/Items?parentId=music&recursive=true&includeItemTypes=MusicAlbum,Audio"); len(page.Items) != 3 {
			t.Errorf("parentId=music returned %v, want the album and its 2 tracks", page.names())
		}
	})

	t.Run("a mixed search spans libraries", func(t *testing.T) {
		page := s.items(t, "/Items?searchTerm=e&recursive=true&includeItemTypes=Movie,MusicAlbum,MusicArtist,Audio&sortBy=SortName")
		types := map[string]int{}
		for _, item := range page.Items {
			types[item.Type]++
		}
		if types["Movie"] == 0 || types["MusicAlbum"] == 0 || types["MusicArtist"] == 0 || types["Audio"] == 0 {
			t.Errorf("search results by type = %v, want every requested type represented", types)
		}
	})

	t.Run("legacy /Users/{id}/Items keeps returning unplayable items", func(t *testing.T) {
		// The web UI and magicboxie-appletv still use this path, and the web
		// UI shows in-progress movies ("Continue Processing"); Jellyfin's own
		// /Items lists only what can actually be played.
		r := s.get("/Users/" + uid + "/Items?IncludeItemTypes=Movie&Recursive=true")
		if r.Status != http.StatusOK {
			t.Fatalf("status = %d", r.Status)
		}
		var page struct {
			Items []struct {
				Name             string
				MagicBoxieStatus string
			}
		}
		r.decode(t, &page)
		byName := map[string]string{}
		for _, item := range page.Items {
			byName[item.Name] = item.MagicBoxieStatus
		}
		if byName["Inception"] != "transcoding" || len(byName) != 4 {
			t.Errorf("legacy listing = %v, want all 4 movies including Inception (transcoding)", byName)
		}
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestItemDetail(t *testing.T) {
	s := newTestServer(t)

	t.Run("movie", func(t *testing.T) {
		r := s.get("/Items/" + idToyStory + "?userId=" + s.userID)
		requireOperation(t, "GetItem", http.StatusOK, r)
		var item struct {
			Id, Name, Type, MediaType, ServerId, SortName, Overview string
			ProductionYear                                          int
			RunTimeTicks                                            int64
			Genres                                                  []string
			ProviderIds                                             map[string]string
			ImageTags                                               map[string]string
			BackdropImageTags                                       []string
			People                                                  []struct{ Id, Name, Type, Role string }
			MediaStreams                                            []struct{ Type, Codec string }
		}
		r.decode(t, &item)

		if item.Id != idToyStory || item.Name != "Toy Story" || item.Type != "Movie" || item.MediaType != "Video" {
			t.Errorf("identity fields wrong: %+v", item)
		}
		if item.ServerId != "magicboxie" || item.SortName != "toy story" {
			t.Errorf("ServerId=%q SortName=%q", item.ServerId, item.SortName)
		}
		if item.ProductionYear != 1995 || item.RunTimeTicks != 4860*10_000_000 {
			t.Errorf("ProductionYear=%d RunTimeTicks=%d, want 1995 and 48600000000 (100ns ticks)", item.ProductionYear, item.RunTimeTicks)
		}
		if !reflect.DeepEqual(item.Genres, []string{"Animation", "Family", "Comedy"}) {
			t.Errorf("Genres = %v", item.Genres)
		}
		if item.ProviderIds["Tmdb"] != "862" {
			t.Errorf("ProviderIds = %v, want Tmdb=862", item.ProviderIds)
		}
		if item.ImageTags["Primary"] == "" || len(item.BackdropImageTags) != 1 {
			t.Errorf("ImageTags=%v BackdropImageTags=%v, want a Primary tag and one backdrop tag", item.ImageTags, item.BackdropImageTags)
		}
		if len(item.People) != 2 || item.People[0].Name != "Tom Hanks" || item.People[0].Type != "Actor" || item.People[0].Role != "Woody" {
			t.Errorf("People = %+v", item.People)
		}
		if item.People[0].Id == "" || item.People[0].Id == item.People[1].Id {
			t.Errorf("each person needs a distinct, stable Id: %+v", item.People)
		}
		if len(item.MediaStreams) != 2 || item.MediaStreams[0].Type != "Video" || item.MediaStreams[0].Codec != "h264" || item.MediaStreams[1].Type != "Audio" {
			t.Errorf("MediaStreams = %+v", item.MediaStreams)
		}
	})

	t.Run("in-progress movie still resolves (clients need its state)", func(t *testing.T) {
		requireOperation(t, "GetItem", http.StatusOK, s.get("/Items/"+idInception))
	})

	t.Run("music items", func(t *testing.T) {
		for _, id := range []string{idArtist, idAlbum, idTrack1} {
			requireOperation(t, "GetItem", http.StatusOK, s.get("/Items/"+id))
		}
	})

	t.Run("library views resolve as items", func(t *testing.T) {
		for id, wantType := range map[string]string{"movies": "movies", "music": "music"} {
			r := s.get("/Items/" + id)
			requireOperation(t, "GetItem", http.StatusOK, r)
			var item itemSummary
			r.decode(t, &item)
			if item.Type != "CollectionFolder" || item.CollectionType != wantType {
				t.Errorf("/Items/%s = %+v, want a CollectionFolder of type %s", id, item, wantType)
			}
		}
	})

	t.Run("legacy path returns the identical item", func(t *testing.T) {
		modern := s.get("/Items/" + idToyStory)
		legacy := s.get("/Users/" + s.userID + "/Items/" + idToyStory)
		if !bytes.Equal(modern.Body, legacy.Body) {
			t.Errorf("GET /Users/{id}/Items/{itemId} differs from GET /Items/{itemId}\nlegacy: %s\nmodern: %s", legacy.Body, modern.Body)
		}
	})

	t.Run("unknown items 404", func(t *testing.T) {
		for _, id := range []string{"movie-999", "track-999", "album-999", "artist-999"} {
			r := s.get("/Items/" + id)
			if r.Status != http.StatusNotFound {
				t.Errorf("GET /Items/%s = %d, want 404", id, r.Status)
			}
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("GET /Items/%s 404 has Content-Type %q, want JSON", id, ct)
			}
		}
	})
}

func TestPlayback(t *testing.T) {
	s := newTestServer(t)

	type mediaSource struct {
		Id                   string
		Container            string
		Protocol             string
		SupportsDirectPlay   bool
		SupportsDirectStream bool
		SupportsTranscoding  bool
		MediaStreams         []struct{ Type, Codec string }
	}
	type playbackInfo struct {
		MediaSources  []mediaSource
		PlaySessionId string
		ErrorCode     string
	}

	// Jellyfin clients negotiate with a device profile, but the response
	// doesn't depend on it here: everything ready is directly playable.
	profile := map[string]any{"DeviceProfile": map[string]any{"MaxStreamingBitrate": 120_000_000}}
	for name, r := range map[string]*response{
		"GET":  s.get("/Items/" + idToyStory + "/PlaybackInfo?userId=" + s.userID),
		"POST": s.post("/Items/"+idToyStory+"/PlaybackInfo?userId="+s.userID, profile),
	} {
		t.Run("playback info "+name+" for a ready movie", func(t *testing.T) {
			op := "GetPlaybackInfo"
			if name == "POST" {
				op = "GetPostedPlaybackInfo"
			}
			requireOperation(t, op, http.StatusOK, r)
			var info playbackInfo
			r.decode(t, &info)
			if len(info.MediaSources) != 1 || info.PlaySessionId == "" {
				t.Fatalf("want exactly one media source and a PlaySessionId: %+v", info)
			}
			src := info.MediaSources[0]
			if !src.SupportsDirectPlay || !src.SupportsDirectStream || src.SupportsTranscoding {
				t.Errorf("DirectPlay=%v DirectStream=%v Transcoding=%v, want true/true/false (files are served as-is)",
					src.SupportsDirectPlay, src.SupportsDirectStream, src.SupportsTranscoding)
			}
			if src.Container != "mp4" || src.Protocol != "File" || src.Id == "" {
				t.Errorf("Container=%q Protocol=%q Id=%q, want mp4/File/non-empty (container comes from the real file)", src.Container, src.Protocol, src.Id)
			}
			if len(src.MediaStreams) != 2 {
				t.Errorf("MediaStreams = %+v, want the video and audio streams", src.MediaStreams)
			}
		})
	}

	t.Run("an unplayable movie offers no media source", func(t *testing.T) {
		r := s.get("/Items/" + idInception + "/PlaybackInfo")
		requireOperation(t, "GetPlaybackInfo", http.StatusOK, r)
		var info playbackInfo
		r.decode(t, &info)
		if len(info.MediaSources) != 0 || info.ErrorCode != "NoCompatibleStream" {
			t.Errorf("got %+v, want no sources and ErrorCode NoCompatibleStream", info)
		}
	})

	t.Run("video stream", func(t *testing.T) {
		path := "/Videos/" + idToyStory + "/stream?static=true&mediaSourceId=" + idToyStory + "&api_key=" + url.QueryEscape(s.token)
		full := s.get(path, noAuth)
		if full.Status != http.StatusOK || !bytes.Equal(full.Body, s.movieFile) {
			t.Fatalf("full GET: status=%d, %d bytes (want 200 and %d bytes)", full.Status, len(full.Body), len(s.movieFile))
		}
		if full.Header.Get("Accept-Ranges") != "bytes" {
			t.Errorf("Accept-Ranges = %q, want bytes (players seek with Range requests)", full.Header.Get("Accept-Ranges"))
		}

		part := s.get(path, noAuth, withHeader("Range", "bytes=16-31"))
		if part.Status != http.StatusPartialContent || !bytes.Equal(part.Body, s.movieFile[16:32]) {
			t.Errorf("range GET: status=%d body=%q, want 206 and bytes 16-31", part.Status, part.Body)
		}
		if want := fmt.Sprintf("bytes 16-31/%d", len(s.movieFile)); part.Header.Get("Content-Range") != want {
			t.Errorf("Content-Range = %q, want %q", part.Header.Get("Content-Range"), want)
		}

		head := s.do(http.MethodHead, path, nil, noAuth)
		if head.Status != http.StatusOK || head.Header.Get("Content-Length") != fmt.Sprint(len(s.movieFile)) || len(head.Body) != 0 {
			t.Errorf("HEAD: status=%d Content-Length=%q body=%d bytes, want 200, %d and empty", head.Status, head.Header.Get("Content-Length"), len(head.Body), len(s.movieFile))
		}
	})

	t.Run("audio stream", func(t *testing.T) {
		path := "/Audio/" + idTrack1 + "/stream?static=true&api_key=" + url.QueryEscape(s.token)
		part := s.get(path, noAuth, withHeader("Range", "bytes=0-9"))
		if part.Status != http.StatusPartialContent || !bytes.Equal(part.Body, s.trackFile[:10]) {
			t.Errorf("range GET: status=%d body=%q, want 206 and the first 10 bytes", part.Status, part.Body)
		}
	})

	t.Run("playback reporting", func(t *testing.T) {
		start := map[string]any{"ItemId": idToyStory, "PlaySessionId": "abc", "CanSeek": true}
		progress := map[string]any{"ItemId": idToyStory, "PlaySessionId": "abc", "PositionTicks": 100_000_000, "IsPaused": false}
		stopped := map[string]any{"ItemId": idToyStory, "PlaySessionId": "abc", "PositionTicks": 200_000_000}
		for path, body := range map[string]any{
			"/Sessions/Playing":          start,
			"/Sessions/Playing/Progress": progress,
			"/Sessions/Playing/Stopped":  stopped,
		} {
			if r := s.post(path, body); r.Status/100 != 2 {
				t.Errorf("POST %s = %d, want 2xx\nbody: %s", path, r.Status, r.Body)
			}
			if r := s.post(path, body, noAuth); r.Status != http.StatusUnauthorized {
				t.Errorf("POST %s without a token = %d, want 401", path, r.Status)
			}
		}
	})

	t.Run("capability report", func(t *testing.T) {
		if r := s.post("/Sessions/Capabilities/Full", map[string]any{"PlayableMediaTypes": []string{"Video", "Audio"}}); r.Status != http.StatusNoContent {
			t.Errorf("POST /Sessions/Capabilities/Full = %d, want 204", r.Status)
		}
	})
}

func TestImages(t *testing.T) {
	s := newTestServer(t)

	t.Run("primary image needs no token", func(t *testing.T) {
		// <img>-style loaders can't attach credentials.
		r := s.get("/Items/"+idToyStory+"/Images/Primary?tag=abc&maxWidth=300&quality=90", noAuth)
		if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(r.Body, s.poster) {
			t.Fatalf("status=%d Content-Type=%q (%d bytes), want the 200 image/jpeg poster", r.Status, r.Header.Get("Content-Type"), len(r.Body))
		}
		if r.Header.Get("Cache-Control") == "" {
			t.Errorf("missing Cache-Control on a tag-versioned image")
		}
	})

	t.Run("backdrops", func(t *testing.T) {
		for _, path := range []string{"/Items/" + idToyStory + "/Images/Backdrop/0", "/Items/" + idToyStory + "/Images/Backdrop"} {
			if r := s.get(path, noAuth); r.Status != http.StatusOK || r.Header.Get("Content-Type") != "image/jpeg" {
				t.Errorf("GET %s = %d %q, want a 200 image/jpeg", path, r.Status, r.Header.Get("Content-Type"))
			}
		}
	})

	t.Run("HEAD works like GET without a body", func(t *testing.T) {
		r := s.do(http.MethodHead, "/Items/"+idToyStory+"/Images/Primary", nil, noAuth)
		if r.Status != http.StatusOK || len(r.Body) != 0 {
			t.Errorf("HEAD = %d with %d body bytes, want 200 and empty", r.Status, len(r.Body))
		}
	})

	t.Run("missing images 404 as JSON, never HTML", func(t *testing.T) {
		// Clients ask for whatever image types their layout wants (Logo, Thumb,
		// Banner...) and treat a 404 as "none available".
		for _, path := range []string{
			"/Items/" + idHeat + "/Images/Primary", // no poster on this movie
			"/Items/" + idToyStory + "/Images/Logo",
			"/Items/" + idToyStory + "/Images/Thumb",
			"/Items/movie-999/Images/Primary",
		} {
			r := s.get(path, noAuth)
			if r.Status != http.StatusNotFound || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				t.Errorf("GET %s = %d %q, want a JSON 404", path, r.Status, r.Header.Get("Content-Type"))
			}
		}
	})
}

// TestKnownDeviations pins each place this server intentionally differs from
// Jellyfin. Every test here asserts the deviation *still exists*: when one is
// fixed the test fails, which is the prompt to delete the matching allowance
// (in the harness or above) rather than let it linger.
func TestKnownDeviations(t *testing.T) {
	s := newTestServer(t)

	t.Run("item ids are not UUIDs", func(t *testing.T) {
		// The spec types every item Id as a UUID. Ours are "<kind>-<n>", which
		// string-typed clients (Swiftfin, jellyfin-web) accept but UUID-typed
		// ones (the Kotlin SDK behind the Android apps) reject. Moving to real
		// UUIDs means changing the id scheme used by the web UI, magicboxie-
		// appletv and magicboxie-device -- a deliberate, cross-client change.
		var item itemSummary
		s.get("/Items/"+idToyStory).decode(t, &item)
		if uuidPattern.MatchString(item.Id) {
			t.Errorf("item ids are now UUIDs (%s): delete magicBoxieIDRe from the harness and this test", item.Id)
		}
	})

	t.Run("playback reporting returns 200 with a body instead of 204", func(t *testing.T) {
		// Kept because magicboxie-appletv was written against these responses;
		// generated clients treat any 2xx as success.
		for _, path := range []string{"/Sessions/Playing", "/Sessions/Playing/Progress", "/Sessions/Playing/Stopped"} {
			if r := s.post(path, map[string]any{"ItemId": idToyStory}); r.Status == http.StatusNoContent {
				t.Errorf("POST %s now returns 204: delete this deviation", path)
			}
		}
	})

	t.Run("the WebSocket endpoint is stubbed", func(t *testing.T) {
		// Clients open /socket for real-time session events; we don't provide
		// any, so they must fall back to polling.
		if r := s.get("/socket"); r.Status != http.StatusNotFound {
			t.Errorf("GET /socket = %d; it now does something, so document/test the real behaviour", r.Status)
		}
	})
}

// TestFirstPartyClientsStillWork replays what MagicBoxie's own clients send.
// They predate the modern Jellyfin paths and can't be run here, so this pins
// their exact requests: the web UI's hooks (useMovies.ts, useMusic.ts,
// api/client.ts -- PascalCase query keys, a Bearer token, a hard-coded "1"
// user id) and magicboxie-appletv's "latest" call.
func TestFirstPartyClientsStillWork(t *testing.T) {
	s := newTestServer(t)
	bearer := withHeader("Authorization", "Bearer "+s.token)

	names := func(t *testing.T, target string) []string {
		t.Helper()
		r := s.get(target, bearer)
		if r.Status != http.StatusOK {
			t.Fatalf("GET %s = %d\nbody: %s", target, r.Status, r.Body)
		}
		var page itemsPage
		r.decode(t, &page)
		return page.names()
	}

	t.Run("web UI movie list includes in-progress movies", func(t *testing.T) {
		got := names(t, "/Users/1/Items?IncludeItemTypes=Movie&Recursive=true")
		if len(got) != 4 {
			t.Errorf("got %v, want all 4 movies (the home page's \"Continue Processing\" row needs Inception)", got)
		}
	})

	t.Run("web UI movie detail", func(t *testing.T) {
		r := s.get("/Users/1/Items/"+idToyStory, bearer)
		var item struct{ Name, MagicBoxieStatus string }
		r.decode(t, &item)
		if r.Status != http.StatusOK || item.Name != "Toy Story" || item.MagicBoxieStatus != "ready" {
			t.Errorf("status=%d item=%+v", r.Status, item)
		}
	})

	t.Run("web UI music pages", func(t *testing.T) {
		if got := names(t, "/Users/1/Items?IncludeItemTypes=MusicArtist"); !reflect.DeepEqual(got, []string{"Miles Davis"}) {
			t.Errorf("artists = %v", got)
		}
		if got := names(t, "/Users/1/Items?IncludeItemTypes=MusicAlbum&ParentId="+idArtist); !reflect.DeepEqual(got, []string{"Kind of Blue"}) {
			t.Errorf("albums = %v", got)
		}
		if got := names(t, "/Users/1/Items?IncludeItemTypes=Audio&ParentId="+idAlbum); !reflect.DeepEqual(got, []string{"So What", "Freddie Freeloader"}) {
			t.Errorf("tracks = %v (want disc/track order with no sortBy)", got)
		}
		for _, id := range []string{idArtist, idAlbum} {
			if r := s.get("/Users/1/Items/"+id, bearer); r.Status != http.StatusOK {
				t.Errorf("GET /Users/1/Items/%s = %d", id, r.Status)
			}
		}
	})

	t.Run("web UI images and streams use the token in the URL", func(t *testing.T) {
		if r := s.get("/Videos/"+idToyStory+"/stream?static=true&api_key="+url.QueryEscape(s.token), noAuth); r.Status != http.StatusOK {
			t.Errorf("stream = %d", r.Status)
		}
		if r := s.get("/Items/"+idToyStory+"/Images/Backdrop/0?tag=x", noAuth); r.Status != http.StatusOK {
			t.Errorf("backdrop = %d", r.Status)
		}
	})

	t.Run("appletv's latest call returns a bare array of playable movies", func(t *testing.T) {
		r := s.get("/Users/"+s.userID+"/Items/Latest?Limit=2", withHeader("Authorization", mediaBrowser(s.token)))
		var latest []itemSummary
		r.decode(t, &latest)
		if r.Status != http.StatusOK || len(latest) != 2 || latest[0].Name != "Heat" {
			t.Errorf("status=%d latest=%+v, want the 2 newest ready movies, Heat first", r.Status, latest)
		}
	})

	t.Run("device Pis can still check in without a token", func(t *testing.T) {
		r := s.post("/devices/register", map[string]string{"device_id": "pi-livingroom"}, noAuth)
		if r.Status != http.StatusOK {
			t.Fatalf("status = %d\nbody: %s", r.Status, r.Body)
		}
		var page itemsPage
		r.decode(t, &page)
		if len(page.Items) != 0 {
			t.Errorf("nothing is marked for sync, so the device should be offered nothing: %v", page.names())
		}
	})
}

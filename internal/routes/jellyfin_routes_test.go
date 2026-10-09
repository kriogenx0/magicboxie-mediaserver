package routes_test

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// The route-table checks compare what the router serves with Jellyfin's
// published operations, in both directions:
//
//   - every route we register must be a real Jellyfin operation, or be
//     explicitly classified below as a MagicBoxie extension / kept-for-
//     compatibility legacy route (so nothing drifts off-spec unnoticed);
//   - every operation real clients call during a normal session must be
//     served (so a missing endpoint fails here, not on someone's Apple TV).

// legacyRoutes are Jellyfin routes that no longer exist in the current spec
// but are deliberately still served: MagicBoxie's own web frontend and
// magicboxie-appletv were built against them. Modern clients use the
// replacements named in each reason.
var legacyRoutes = map[string]string{
	"GET /Users/:userId/Views":         "removed from Jellyfin; replaced by GET /UserViews",
	"GET /Users/:userId/Items":         "removed from Jellyfin; replaced by GET /Items (still used by the web frontend and magicboxie-appletv)",
	"GET /Users/:userId/Items/Latest":  "removed from Jellyfin; replaced by GET /Items/Latest (still used by magicboxie-appletv)",
	"GET /Users/:userId/Items/:itemId": "removed from Jellyfin; replaced by GET /Items/:itemId (still used by the web frontend)",
}

// extensionRoutes are MagicBoxie-specific endpoints with no Jellyfin
// equivalent. Everything under /api/ is one; the rest are listed by name.
var extensionRoutes = map[string]string{
	"POST /devices/register":      "magicboxie-device Pi check-in",
	"GET /Videos/:itemId/preview": "short muted preview clip for the web UI",
	"GET /Videos/:itemId/player":  "480p copy magicboxie-player downloads",
	"GET /socket":                 "Jellyfin's WebSocket endpoint; not an OpenAPI operation. Stubbed as 404",
}

// clientFlow lists, by Jellyfin operation, what real clients call during a
// normal session. Swiftfin's list is taken from its SDK's request paths;
// each entry is also asserted to exist in the spec, so this list can't
// itself drift from Jellyfin.
var clientFlow = []struct{ Method, SpecPath, Why string }{
	// connecting and signing in
	{"GET", "/System/Info/Public", "server discovery before the login screen"},
	{"GET", "/Users/Public", "login screen user list"},
	{"POST", "/Users/AuthenticateByName", "login"},
	{"GET", "/QuickConnect/Enabled", "login screen option"},
	{"GET", "/Branding/Configuration", "login screen branding"},
	{"GET", "/System/Info", "post-login server details"},
	{"GET", "/System/Ping", "connectivity check"},
	{"GET", "/Users/Me", "current user"},
	{"GET", "/Users/{userId}", "user by id (older SDKs)"},
	// home screen
	{"GET", "/UserViews", "library tiles / home rows"},
	{"GET", "/Items/Latest", "\"Latest in <library>\" rows"},
	{"GET", "/UserItems/Resume", "\"Continue Watching\" row"},
	{"GET", "/Shows/NextUp", "\"Next Up\" row"},
	// browsing
	{"GET", "/Items", "every library/tab/search listing"},
	{"GET", "/Items/{itemId}", "item detail"},
	{"GET", "/Items/{itemId}/Images/{imageType}", "posters and backdrops"},
	// playback
	{"GET", "/Items/{itemId}/PlaybackInfo", "playback negotiation"},
	{"POST", "/Items/{itemId}/PlaybackInfo", "playback negotiation with a device profile"},
	{"GET", "/Videos/{itemId}/stream", "direct-play video"},
	{"HEAD", "/Videos/{itemId}/stream", "size probe before streaming"},
	{"GET", "/Audio/{itemId}/stream", "direct-play audio"},
	{"POST", "/Sessions/Capabilities/Full", "device capability report at startup"},
	{"POST", "/Sessions/Playing", "playback started"},
	{"POST", "/Sessions/Playing/Progress", "playback progress"},
	{"POST", "/Sessions/Playing/Stopped", "playback stopped"},
}

// matchesSpecPath reports whether one of our gin routes serves the spec
// path. Spec "{param}" segments accept anything we route there (a param or a
// fixed value such as ".../Images/Primary"); fixed spec segments must match
// exactly, and our own ":param" segments may only line up with spec params.
func matchesSpecPath(specPath, ginPath string) bool {
	spec := strings.Split(strings.Trim(specPath, "/"), "/")
	ours := strings.Split(strings.Trim(ginPath, "/"), "/")
	if len(spec) != len(ours) {
		return false
	}
	for i := range spec {
		switch {
		case strings.Contains(spec[i], "{"):
			continue
		case strings.HasPrefix(ours[i], ":"):
			return false
		case spec[i] != ours[i]:
			return false
		}
	}
	return true
}

func (s *testServer) registeredRoutes() []struct{ Method, Path string } {
	var out []struct{ Method, Path string }
	for _, r := range s.router.Routes() {
		out = append(out, struct{ Method, Path string }{r.Method, r.Path})
	}
	return out
}

func TestEveryServedRouteIsJellyfinOrDeclared(t *testing.T) {
	s := newTestServer(t)
	spec := specOperations(t)

	for _, route := range s.registeredRoutes() {
		key := route.Method + " " + route.Path
		if _, ok := legacyRoutes[key]; ok {
			continue
		}
		if _, ok := extensionRoutes[key]; ok {
			continue
		}
		if strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		found := false
		for _, op := range spec {
			if op.Method == route.Method && matchesSpecPath(op.Path, route.Path) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is not a Jellyfin operation (Jellyfin %s). If it's intentional, classify it in "+
				"legacyRoutes or extensionRoutes with a reason; otherwise it's a typo or an outdated path",
				key, jellyfinSpec(t).Info.Version)
		}
	}
}

func TestLegacyRoutesAreStillAbsentFromJellyfin(t *testing.T) {
	// If Jellyfin ever re-adds one of these, it stops being "legacy" and
	// should move to the conformance check above.
	s := newTestServer(t)
	spec := specOperations(t)
	served := map[string]bool{}
	for _, route := range s.registeredRoutes() {
		served[route.Method+" "+route.Path] = true
	}
	for key, reason := range legacyRoutes {
		if !served[key] {
			t.Errorf("legacyRoutes lists %q (%s) but the router no longer serves it; remove the entry", key, reason)
		}
		method, path, _ := strings.Cut(key, " ")
		for _, op := range spec {
			if op.Method == method && matchesSpecPath(op.Path, path) {
				t.Errorf("legacyRoutes lists %q but Jellyfin %s defines %s %s; it is no longer legacy",
					key, jellyfinSpec(t).Info.Version, op.Method, op.Path)
			}
		}
	}
}

func TestClientFlowEndpointsAreServed(t *testing.T) {
	s := newTestServer(t)
	spec := specOperations(t)
	routes := s.registeredRoutes()

	for _, need := range clientFlow {
		inSpec := false
		for _, op := range spec {
			if op.Method == need.Method && op.Path == need.SpecPath {
				inSpec = true
			}
		}
		if !inSpec {
			t.Errorf("clientFlow lists %s %s but Jellyfin %s has no such operation; fix the list",
				need.Method, need.SpecPath, jellyfinSpec(t).Info.Version)
			continue
		}
		served := false
		for _, route := range routes {
			if route.Method == need.Method && matchesSpecPath(need.SpecPath, route.Path) {
				served = true
				break
			}
		}
		if !served {
			t.Errorf("missing endpoint: %s %s (%s) is called by Jellyfin clients but not served",
				need.Method, need.SpecPath, need.Why)
		}
	}
}

// Unknown Jellyfin endpoints must 404 as JSON. If they fall through to the
// SPA's index.html they answer 200 text/html, which generated clients then
// fail to decode with an opaque "data format" error instead of a clean 404.
func TestUnroutedJellyfinPathsNeverServeHTML(t *testing.T) {
	s := newTestServer(t)

	served := map[string]bool{}
	for _, route := range s.registeredRoutes() {
		served["/"+strings.SplitN(strings.Trim(route.Path, "/"), "/", 2)[0]] = true
	}

	segments := map[string]bool{}
	for _, op := range specOperations(t) {
		first := strings.SplitN(strings.Trim(op.Path, "/"), "/", 2)[0]
		if !strings.Contains(first, "{") {
			segments["/"+first] = true
		}
	}
	var paths []string
	for seg := range segments {
		paths = append(paths, seg, seg+"/x")
	}
	sort.Strings(paths)

	for _, path := range paths {
		r := s.get(path)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
			t.Errorf("GET %s returned HTML (status %d); Jellyfin API paths must never fall through to the SPA", path, r.Status)
		}
	}
	// ...while the web app's own client-side routes still do.
	for _, path := range []string{"/", "/login", "/movies/5", "/music/artists/2", "/admin/movies"} {
		r := s.get(path, noAuth)
		if r.Status != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
			t.Errorf("GET %s = %d %q; SPA routes must still serve index.html", path, r.Status, r.Header.Get("Content-Type"))
		}
	}
}

func TestAdvertisedVersionMatchesVendoredSpec(t *testing.T) {
	s := newTestServer(t)
	specVersion := jellyfinSpec(t).Info.Version

	r := s.get("/System/Info/Public", noAuth)
	requireOperation(t, "GetPublicSystemInfo", http.StatusOK, r)
	var info struct{ Version string }
	r.decode(t, &info)
	if info.Version != specVersion {
		t.Errorf("server advertises Jellyfin %s but the vendored spec is %s; bump controllers.JellyfinServerVersion "+
			"and rerun scripts/update-jellyfin-spec.sh together", info.Version, specVersion)
	}
}

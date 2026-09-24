package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterSPA serves the embedded frontend, falling back to index.html for
// unknown browser route so client-side routing survives a page refresh.
func RegisterSPA(router *gin.Engine, fsys http.FileSystem) {
	fileServer := http.FileServer(fsys)

	router.NoRoute(func(c *gin.Context) {
		if isAPIPath(c.Request.URL.Path) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		reqPath := strings.TrimPrefix(c.Request.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}

		if f, err := fsys.Open(reqPath); err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

// isAPIPath reports whether path belongs to the server's API rather than the
// web app. Unknown API paths must 404 as JSON: falling through to index.html
// answers 200 text/html, which generated Jellyfin clients then fail to decode
// with an opaque "data format" error instead of a clean 404.
//
// The two namespaces split cleanly by case. Every Jellyfin endpoint's first
// path segment is capitalised (/Items, /Users, /System, /Genres ...) while all
// of the web app's routes and assets are lower-case (/login, /movies, /music,
// /admin, /assets). That rule covers Jellyfin's whole API -- including
// endpoints we don't serve -- without a list to keep in step with it; the
// remaining lower-case prefixes are MagicBoxie's own API, Jellyfin's
// WebSocket, and Jellyfin's one lower-case REST path (/web/ConfigurationPage,
// its plugin admin pages).
func isAPIPath(path string) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if first == "" {
		return false
	}
	if c := first[0]; c >= 'A' && c <= 'Z' {
		return true
	}
	switch first {
	case "api", "socket", "devices", "web":
		return true
	}
	return false
}

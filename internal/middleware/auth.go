package middleware

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"magicboxie/internal/auth"
)

var mediaBrowserTokenRe = regexp.MustCompile(`Token="([^"]*)"`)

// RequireAuth accepts every token transport Jellyfin's own server does, plus
// a plain bearer token, in priority order:
//  1. A "MediaBrowser ...Token="..."" credential in the Authorization header
//     (Swiftfin, the Kotlin SDK, magicboxie-appletv) or in X-Emby-Authorization
//     (older clients and SDKs, jellyfin-web, Kodi).
//  2. The X-Emby-Token or X-MediaBrowser-Token header.
//  3. A plain "Authorization: Bearer <token>" header (MagicBoxie's web UI).
//  4. A "?api_key=<token>" (or "?ApiKey=") query param -- Jellyfin's convention
//     for URLs that can't carry headers (<video>/<img> tags, AVPlayer,
//     EventSource).
//
// A LAN-only token (issued without a password, see auth.IssueLANToken) is
// rejected unless the request also comes from the LAN.
func RequireAuth(manager *auth.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		lanOnly, err := manager.VerifyToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if lanOnly && !IsLAN(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "this token only works on the home network"})
			return
		}
		c.Next()
	}
}

// RequireAuthWhenPublic leaves an endpoint open on the LAN (and wherever the
// network isn't known), but asks requests through the public address for a
// token like any other.
func RequireAuthWhenPublic(manager *auth.Manager) gin.HandlerFunc {
	requireAuth := RequireAuth(manager)
	return func(c *gin.Context) {
		if IsPublic(c) {
			requireAuth(c)
			return
		}
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	for _, name := range []string{"Authorization", "X-Emby-Authorization"} {
		if header := c.GetHeader(name); strings.HasPrefix(header, "MediaBrowser ") {
			if m := mediaBrowserTokenRe.FindStringSubmatch(header); m != nil {
				return m[1]
			}
		}
	}
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if token := c.GetHeader(name); token != "" {
			return token
		}
	}
	if header := c.GetHeader("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	for key, values := range c.Request.URL.Query() {
		if (strings.EqualFold(key, "api_key") || strings.EqualFold(key, "ApiKey")) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

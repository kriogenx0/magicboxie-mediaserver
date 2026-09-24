package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"magicboxie/internal/auth"
	"magicboxie/internal/config"
)

type AuthController struct {
	cfg     *config.Config
	manager *auth.Manager
}

func NewAuthController(cfg *config.Config, manager *auth.Manager) *AuthController {
	return &AuthController{cfg: cfg, manager: manager}
}

// localAddress is how this server was reached, which is what Jellyfin reports
// as its LocalAddress (clients use it to detect a moved or renamed server).
func localAddress(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

// SystemInfoPublic is the unauthenticated "is this a real server" endpoint
// Jellyfin clients (Swiftfin, magicboxie-appletv's AddServerView...) call
// before showing a login screen.
func (a *AuthController) SystemInfoPublic(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"LocalAddress":           localAddress(c),
		"ServerName":             "MagicBoxie",
		"Version":                JellyfinServerVersion,
		"ProductName":            "Jellyfin Server",
		"OperatingSystem":        "Linux",
		"StartupWizardCompleted": true,
		"Id":                     jellyfinServerID,
	})
}

// SystemInfo is the authenticated GET /System/Info clients fetch after login.
// It reports a server with nothing pending and no way to restart itself.
func (a *AuthController) SystemInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"LocalAddress":           localAddress(c),
		"ServerName":             "MagicBoxie",
		"Version":                JellyfinServerVersion,
		"ProductName":            "Jellyfin Server",
		"OperatingSystem":        "Linux",
		"StartupWizardCompleted": true,
		"Id":                     jellyfinServerID,
		"HasPendingRestart":      false,
		"IsShuttingDown":         false,
		"SupportsLibraryMonitor": false,
		"CanSelfRestart":         false,
		"CanLaunchWebBrowser":    false,
		"WebSocketPortNumber":    0,
	})
}

// Ping answers GET/POST /System/Ping, which clients use as a cheap
// "is the server still there" check. Jellyfin replies with this exact string.
func (a *AuthController) Ping(c *gin.Context) {
	c.JSON(http.StatusOK, "Jellyfin Server")
}

// PublicUsers returns the single shared account in Jellyfin's UserDto shape.
// Clients use this endpoint to populate the login screen.
func (a *AuthController) PublicUsers(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{jellyfinUser("admin")})
}

// CurrentUser returns the authenticated shared account. A complete-enough
// UserDto is important here: generated Jellyfin clients decode several of
// these fields as non-optional values.
func (a *AuthController) CurrentUser(c *gin.Context) {
	c.JSON(http.StatusOK, jellyfinUser("admin"))
}

// UserByID is GET /Users/{userId}. There is exactly one account, so any other
// id is an unknown user. The pre-UUID id "1" is still accepted because the
// web UI and magicboxie-appletv were built with it hard-coded.
func (a *AuthController) UserByID(c *gin.Context) {
	if id := c.Param("userId"); id != jellyfinUserID && id != "1" {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, jellyfinUser("admin"))
}

// jellyfinUserID is the shared account's id. Jellyfin user ids are UUIDs and
// UUID-typed SDKs refuse anything else, so this is a fixed, valid one.
var jellyfinUserID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("magicboxie/user")).String()

func jellyfinUser(name string) gin.H {
	return gin.H{
		"Name":                      name,
		"ServerId":                  jellyfinServerID,
		"Id":                        jellyfinUserID,
		"HasPassword":               true,
		"HasConfiguredPassword":     true,
		"HasConfiguredEasyPassword": false,
		"EnableAutoLogin":           false,
		"Configuration": gin.H{
			"PlayDefaultAudioTrack":      true,
			"SubtitleMode":               "Default",
			"DisplayMissingEpisodes":     false,
			"GroupedFolders":             []string{},
			"OrderedViews":               []string{},
			"LatestItemsExcludes":        []string{},
			"MyMediaExcludes":            []string{},
			"HidePlayedInLatest":         true,
			"RememberAudioSelections":    true,
			"RememberSubtitleSelections": true,
		},
		"Policy": gin.H{
			"IsAdministrator":                true,
			"IsHidden":                       false,
			"IsDisabled":                     false,
			"EnableAllFolders":               true,
			"EnableAllDevices":               true,
			"EnableContentDeletion":          true,
			"EnableContentDownloading":       true,
			"EnableMediaPlayback":            true,
			"EnableAudioPlaybackTranscoding": true,
			"EnableVideoPlaybackTranscoding": true,
			"EnablePlaybackRemuxing":         true,
			"EnableRemoteAccess":             true,
			"EnableLiveTvAccess":             false,
			"EnableLiveTvManagement":         false,
			"EnableSharedDeviceControl":      true,
			"EnableUserPreferenceAccess":     true,
			// Required by Jellyfin's UserPolicy schema (the Swift SDK refuses
			// to decode a user without them): which built-in providers handle
			// this user's password check and reset.
			"AuthenticationProviderId": "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider",
			"PasswordResetProviderId":  "Jellyfin.Server.Implementations.Users.DefaultPasswordResetProvider",
		},
	}
}

type authenticateByNameRequest struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
}

// AuthenticateByName is Jellyfin's login endpoint. Username is accepted and
// echoed back but never validated -- MagicBoxie has one shared password, not
// per-user accounts.
func (a *AuthController) AuthenticateByName(c *gin.Context) {
	var req authenticateByNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username and Pw are required"})
		return
	}

	if !auth.CheckPassword(a.cfg.Auth.PasswordHash, req.Pw) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}

	token, _, err := a.manager.IssueToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
		return
	}

	username := req.Username
	if username == "" {
		username = "magicboxie"
	}

	c.JSON(http.StatusOK, gin.H{
		"AccessToken": token,
		"ServerId":    jellyfinServerID,
		"User":        jellyfinUser(username),
	})
}

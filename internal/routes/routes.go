package routes

import (
	"github.com/gin-gonic/gin"

	"magicboxie/internal/auth"
	"magicboxie/internal/controllers"
	"magicboxie/internal/middleware"
	"magicboxie/internal/services/events"
)

type Dependencies struct {
	AuthManager       *auth.Manager
	AuthController    *controllers.AuthController
	ItemsController   *controllers.ItemsController
	VideosController  *controllers.VideosController
	AudioController   *controllers.AudioController
	UploadsController *controllers.UploadsController
	EventsHub         *events.Hub
}

// Register wires two surfaces onto the router:
//
//  1. A Jellyfin-compatible surface at bare paths (/System, /Users, /Items,
//     /Videos, /Sessions) matching real Jellyfin's conventions exactly, so
//     magicboxie-appletv (already a partial Jellyfin client) and any generic
//     Jellyfin client work against this server unmodified. The conformance
//     tests (jellyfin_*_test.go) check every route here against Jellyfin's
//     published OpenAPI spec; a route that isn't in it must be classified there
//     as a legacy or extension route on purpose.
//  2. MagicBoxie-specific extensions with no Jellyfin equivalent (chunked
//     upload, library scan trigger, SSE job-progress, manual TMDB
//     re-match), namespaced under /api/* so they're clearly separate
//     from the standard surface.
func Register(router *gin.Engine, deps Dependencies) {
	router.GET("/System/Info/Public", deps.AuthController.SystemInfoPublic)
	router.GET("/Users/Public", deps.AuthController.PublicUsers)
	router.GET("/QuickConnect/Enabled", func(c *gin.Context) { c.JSON(200, false) })
	router.GET("/Branding/Configuration", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	router.GET("/Branding/Splashscreen", func(c *gin.Context) { c.Status(404) })
	router.POST("/Users/AuthenticateByName", deps.AuthController.AuthenticateByName)
	router.GET("/socket", func(c *gin.Context) { c.Status(404) })
	router.GET("/System/Ping", deps.AuthController.Ping)
	router.POST("/System/Ping", deps.AuthController.Ping)

	// Unauthenticated check-in for magicboxie-device Pis (see
	// player_app/views/home_sync_service.py in that repo) -- a lower-friction
	// alternative to the Jellyfin login flow above for a headless device that
	// boots straight into opportunistic sync.
	router.POST("/devices/register", deps.ItemsController.RegisterDevice)

	// Images are unauthenticated, matching real Jellyfin's convention (so
	// <img> tags never need a token).
	router.GET("/Items/:itemId/Images/:imageType", deps.ItemsController.ItemImage)
	router.HEAD("/Items/:itemId/Images/:imageType", deps.ItemsController.ItemImage)
	router.GET("/Items/:itemId/Images/:imageType/:imageIndex", deps.ItemsController.ItemImage)
	router.HEAD("/Items/:itemId/Images/:imageType/:imageIndex", deps.ItemsController.ItemImage)

	router.GET("/api/health", controllers.Health)

	authorized := router.Group("")
	authorized.Use(middleware.RequireAuth(deps.AuthManager))
	{
		authorized.GET("/Users/Me", deps.AuthController.CurrentUser)
		authorized.GET("/Users/:userId", deps.AuthController.UserByID)
		authorized.GET("/System/Info", deps.AuthController.SystemInfo)
		authorized.GET("/UserImage", func(c *gin.Context) { c.Status(404) })
		authorized.POST("/Sessions/Capabilities", func(c *gin.Context) { c.Status(204) })
		authorized.POST("/Sessions/Capabilities/Full", func(c *gin.Context) { c.Status(204) })

		// Library browsing, as current Jellyfin clients (Swiftfin, Findroid,
		// jellyfin-web...) call it. GET /Items is the one listing endpoint behind
		// every library, tab and search; the user comes from the token, not the path.
		authorized.GET("/UserViews", deps.ItemsController.Views)
		authorized.GET("/Items", deps.ItemsController.ListItems)
		authorized.GET("/Items/Latest", deps.ItemsController.Latest)
		authorized.GET("/Items/:itemId", deps.ItemsController.Detail)
		authorized.GET("/UserItems/Resume", deps.ItemsController.EmptyItems)
		authorized.GET("/Shows/NextUp", deps.ItemsController.EmptyItems)

		// Pre-10.9 per-user paths, gone from Jellyfin but still used by the web
		// UI and magicboxie-appletv (see legacyRoutes in the conformance tests).
		authorized.GET("/Users/:userId/Views", deps.ItemsController.Views)
		authorized.GET("/Users/:userId/Items", deps.ItemsController.List)
		authorized.GET("/Users/:userId/Items/Latest", deps.ItemsController.Latest)
		authorized.GET("/Users/:userId/Items/:itemId", deps.ItemsController.Detail)

		authorized.GET("/Items/:itemId/PlaybackInfo", deps.ItemsController.PlaybackInfo)
		authorized.POST("/Items/:itemId/PlaybackInfo", deps.ItemsController.PlaybackInfo)

		authorized.GET("/Videos/:itemId/stream", deps.VideosController.Stream)
		authorized.HEAD("/Videos/:itemId/stream", deps.VideosController.Stream)
		authorized.GET("/Videos/:itemId/preview", deps.VideosController.Preview)

		authorized.GET("/Audio/:itemId/stream", deps.AudioController.Stream)
		authorized.HEAD("/Audio/:itemId/stream", deps.AudioController.Stream)

		authorized.POST("/Sessions/Playing", controllers.PlayingStart)
		authorized.POST("/Sessions/Playing/Progress", controllers.PlayingProgress)
		authorized.POST("/Sessions/Playing/Stopped", controllers.PlayingStopped)

		api := authorized.Group("/api")
		{
			api.POST("/library/scan", deps.ItemsController.Scan)
			api.POST("/library/music/scan", deps.ItemsController.MusicScan)
			api.POST("/items/:itemId/match", deps.ItemsController.Match)
			api.PATCH("/items/:itemId", deps.ItemsController.Rename)
			api.DELETE("/items/:itemId", deps.ItemsController.Delete)
			api.GET("/items/:itemId/thumbnails", deps.ItemsController.ThumbnailCandidates)
			api.POST("/items/:itemId/thumbnails/select", deps.ItemsController.SelectThumbnail)
			api.POST("/items/:itemId/sync", deps.ItemsController.SetDeviceSync)
			api.GET("/devices", deps.ItemsController.ListDevices)
			api.GET("/items/search", deps.ItemsController.Search)
			api.GET("/jobs", deps.ItemsController.Jobs)

			api.POST("/uploads", deps.UploadsController.Create)
			api.GET("/uploads/checksum/:sha256", deps.UploadsController.ChecksumStatus)
			api.POST("/uploads/direct", deps.UploadsController.Direct)
			api.GET("/uploads/:id", deps.UploadsController.Status)
			api.PUT("/uploads/:id/chunk", deps.UploadsController.Chunk)
			api.POST("/uploads/:id/complete", deps.UploadsController.Complete)

			api.GET("/events", controllers.Events(deps.EventsHub))
		}
	}
}

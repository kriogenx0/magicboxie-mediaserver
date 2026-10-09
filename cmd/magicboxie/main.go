package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"

	"github.com/gin-gonic/gin"

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
	"magicboxie/internal/services/transcode"
	"magicboxie/internal/services/upload"
	"magicboxie/internal/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		runHashPassword()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "rematch-tmdb" {
		runRematchTMDB(os.Args[2:])
		return
	}

	defaultConfigPath := os.Getenv("MAGICBOXIE_CONFIG")
	if defaultConfigPath == "" {
		defaultConfigPath = "/etc/magicboxie/config.yaml"
	}
	configPath := flag.String("config", defaultConfigPath, "path to config YAML file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("magicboxie: %v", err)
	}

	for _, dir := range []string{cfg.DataDir, cfg.MoviesDir, cfg.MusicDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("magicboxie: creating directory %q: %v", dir, err)
		}
	}

	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			log.Fatalf("magicboxie: required dependency %q not found on PATH: %v", bin, err)
		}
	}

	gormDB, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("magicboxie: %v", err)
	}

	authManager, err := auth.NewManager(cfg.DataDir)
	if err != nil {
		log.Fatalf("magicboxie: %v", err)
	}

	tmdbClient := tmdb.NewClient(cfg.TMDB.APIReadToken)
	importer := library.NewImporter(gormDB, cfg.MoviesDir, cfg.DataDir, tmdbClient)
	musicImporter := music.NewImporter(gormDB, cfg.MusicDir, cfg.DataDir)

	eventsHub := events.NewHub()
	transcodeManager := transcode.NewManager(gormDB, cfg.MoviesDir, cfg.DataDir, cfg.Transcode.Preset, cfg.Transcode.CRF, cfg.Transcode.MaxConcurrentJobs, eventsHub)
	importer.OnNeedsTranscode = transcodeManager.Enqueue
	transcodeManager.Start(context.Background())

	authController := controllers.NewAuthController(cfg, authManager)
	itemsController := controllers.NewItemsController(gormDB, importer, musicImporter, cfg.MoviesDir, cfg.DataDir)
	itemsController.OnSyncEnabled = transcodeManager.EnqueuePlayer
	videosController := controllers.NewVideosController(gormDB, cfg.MoviesDir, cfg.DataDir)
	audioController := controllers.NewAudioController(gormDB, cfg.MusicDir)

	uploadManager, err := upload.NewManager(gormDB, cfg.MoviesDir)
	if err != nil {
		log.Fatalf("magicboxie: %v", err)
	}
	uploadsController := controllers.NewUploadsController(uploadManager, cfg.MoviesDir, cfg.MusicDir, importer, musicImporter)

	router := gin.Default()
	routes.Register(router, routes.Dependencies{
		AuthManager:       authManager,
		AuthController:    authController,
		ItemsController:   itemsController,
		VideosController:  videosController,
		AudioController:   audioController,
		UploadsController: uploadsController,
		InfoController:    controllers.NewInfoController(gormDB, cfg.MoviesDir, cfg.MusicDir, cfg.DataDir),
		EventsHub:         eventsHub,
	})

	fsys, err := web.FileSystem()
	if err != nil {
		log.Fatalf("magicboxie: %v", err)
	}
	web.RegisterSPA(router, fsys)

	log.Printf("magicboxie: listening on %s", cfg.ListenAddr)
	if err := router.Run(cfg.ListenAddr); err != nil {
		log.Fatalf("magicboxie: server error: %v", err)
	}
}

func runRematchTMDB(args []string) {
	flags := flag.NewFlagSet("rematch-tmdb", flag.ExitOnError)
	configPath := flags.String("config", "/etc/magicboxie/config.yaml", "path to config YAML file")
	_ = flags.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	database, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	client := tmdb.NewClient(cfg.TMDB.APIReadToken)
	importer := library.NewImporter(database, cfg.MoviesDir, cfg.DataDir, client)
	var movies []models.Movie
	if err := database.Order("id").Find(&movies).Error; err != nil {
		log.Fatal(err)
	}
	matched, unmatched, failed := 0, 0, 0
	for i := range movies {
		movie := &movies[i]
		results, err := importer.SearchTMDB(context.Background(), movie.Title, movie.Year)
		if err != nil {
			log.Printf("FAILED  %q: %v", movie.Title, err)
			failed++
			continue
		}
		if len(results) == 0 && movie.Year > 0 {
			results, err = importer.SearchTMDB(context.Background(), movie.Title, 0)
		}
		if err != nil || len(results) == 0 {
			log.Printf("NO MATCH %q", movie.Title)
			unmatched++
			continue
		}
		sort.Slice(results, func(i, j int) bool { return results[i].Popularity > results[j].Popularity })
		if err := importer.ApplyManualMatch(context.Background(), movie, results[0].ID); err != nil {
			log.Printf("FAILED  %q: %v", movie.Title, err)
			failed++
			continue
		}
		log.Printf("MATCHED %q -> TMDB %d", movie.Title, results[0].ID)
		matched++
	}
	fmt.Printf("TMDB refresh complete: %d matched, %d unmatched, %d failed\n", matched, unmatched, failed)
}

func runHashPassword() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: magicboxie hash-password <password>")
		os.Exit(1)
	}
	hash, err := auth.HashPassword(os.Args[2])
	if err != nil {
		log.Fatalf("magicboxie: %v", err)
	}
	fmt.Println(hash)
}

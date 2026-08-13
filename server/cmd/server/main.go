package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/handlers"
	"github.com/yuanjing-hash/ohmycine/server/internal/httpserver"
	"github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logManager, err := logging.NewManager(cfg.LogDirectory, cfg.Environment, os.Stdout)
	if err != nil {
		panic(err)
	}
	defer logManager.Close()
	log := logManager.Logger("server", "bootstrap")
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open database")
	}
	if err := database.Migrate(db); err != nil {
		log.Fatal().Err(err).Msg("Failed to migrate database")
	}
	audit := services.NewAuditService(db)
	runtimeLogs, err := services.NewRuntimeLogService(db, logManager, audit)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize runtime logging")
	}
	authorization := services.NewAuthorizationService(db)
	auth, err := services.NewAuthService(db, cfg, authorization, audit)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize authentication")
	}
	admin := services.NewAdminService(db, authorization, auth, audit)
	storages := services.NewStorageService(db, audit)
	directories, err := services.NewDirectoryBrowserService(db, nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize directory browser")
	}
	profiles := services.NewMediaClassificationProfileService(db, audit, nil)
	libraries := services.NewMediaLibraryService(db, audit, logManager.Logger("media_library", "supervisor"))
	profiles.SetReferences(libraries)
	profiles.SetRevisionNotifier(libraries)
	storages.SetReferenceChecker(libraries)
	api := handlers.NewAPI(cfg, auth, admin, audit, storages, directories, profiles, log)
	api.SetRuntimeLogService(runtimeLogs)
	api.SetMediaLibraryService(libraries)
	queue := services.NewQueueService(db, audit)
	queueEvents := services.NewQueueEventHub()
	queue.SetEventHub(queueEvents)
	registry := services.NewWorkerRegistry()
	if cfg.Environment != "production" {
		services.RegisterFakeWorkers(registry)
	}
	scheduler := services.NewScheduler(queue, registry, logManager.Logger("queue", "scheduler"))
	api.SetQueueService(queue)
	api.SetQueueEventHub(queueEvents)
	if err := scheduler.Start(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("Failed to start persistent task scheduler")
	}
	defer scheduler.Close()
	if err := libraries.Start(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("Failed to start media library supervisors")
	}
	defer libraries.Close()
	server := &http.Server{
		Addr: cfg.Address(), Handler: httpserver.New(cfg, api, auth, logManager.Logger("http", "request")),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute,
	}

	go func() {
		log.Info().Str("address", cfg.Address()).Msg("OhMyCine Server started")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("Server stopped unexpectedly")
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	log.Info().Msg("OhMyCine Server stopped")
}

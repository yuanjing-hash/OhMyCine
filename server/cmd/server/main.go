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
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	log := httpserver.NewLogger(cfg.Environment)
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open database")
	}
	if err := database.Migrate(db); err != nil {
		log.Fatal().Err(err).Msg("Failed to migrate database")
	}
	audit := services.NewAuditService(db)
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
	api := handlers.NewAPI(cfg, auth, admin, audit, storages, directories, log)
	server := &http.Server{
		Addr: cfg.Address(), Handler: httpserver.New(cfg, api, auth, log),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute,
	}

	go func() {
		log.Info().Str("address", cfg.Address()).Str("database", cfg.DatabasePath).Msg("OhMyCine Server started")
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

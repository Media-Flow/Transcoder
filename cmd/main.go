package main

import (
	"Transcoder/internal/database"
	"Transcoder/internal/ffmpeg"
	"Transcoder/internal/media"
	"Transcoder/internal/storage"
	"Transcoder/internal/transcoder"
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"Transcoder/cmd/consumer"
	"Transcoder/pkg/config"
	"Transcoder/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	dotenvErr := godotenv.Load()

	cfg, err := config.LoadConfig()
	if err != nil {
		_ = logger.InitLogger(&config.Config{})
		logger.NewLogger("Main").Errorf("load application configuration: %v", err)
		os.Exit(1)
	}

	loggerErr := logger.InitLogger(cfg)
	log := logger.NewLogger("Main")
	if dotenvErr != nil && !errors.Is(dotenvErr, os.ErrNotExist) {
		log.Warnf("load .env file: %v", dotenvErr)
	}
	if loggerErr != nil {
		log.Warnf("logger initialization fell back to console output: %v", loggerErr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.NewDatabase(ctx, *cfg)
	if err != nil {
		log.Errorf("connect database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	mediaRepo := media.NewRepository(db)
	mediaService := media.NewService(mediaRepo, media.DefaultTranscodeLease)

	storageRepo := storage.NewStorageRepository(cfg, cfg.Storage.Region)
	storageService := storage.NewStorageService(storageRepo)

	executor := ffmpeg.NewExecutor()

	transcoderService := transcoder.NewTranscoder(
		executor,
		mediaService,
		storageService,
	)

	dispatcher, err := consumer.NewDispatcher(transcoderService, mediaService)
	if err != nil {
		log.Errorf("create dispatcher: %v", err)
		os.Exit(1)
	}

	if ctx.Err() != nil {
		log.Infof("shutdown requested while dispatcher was starting")
		if err := dispatcher.Stop(); err != nil {
			log.Warnf("stop dispatcher during startup: %v", err)
		}
		return
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- dispatcher.Start()
	}()

	select {
	case err := <-runErr:
		if err != nil {
			log.Errorf("dispatcher stopped unexpectedly: %v", err)
			os.Exit(1)
		}
		log.Infof("dispatcher stopped")

	case <-ctx.Done():
		// graceful shutdown
		stop()
		log.Infof("shutdown signal received; stopping dispatcher")
		if err := dispatcher.Stop(); err != nil {
			log.Warnf("stop dispatcher: %v", err)
		}

		if err := <-runErr; err != nil {
			log.Warnf("dispatcher stopped during shutdown: %v", err)
		}
		log.Infof("shutdown complete")
	}
}

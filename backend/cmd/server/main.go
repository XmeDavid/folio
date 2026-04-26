package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/riverqueue/river"
	"github.com/xmedavid/folio/backend/internal/config"
	"github.com/xmedavid/folio/backend/internal/db"
	folioHTTP "github.com/xmedavid/folio/backend/internal/http"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/mailer"
)

func main() {
	_ = godotenv.Load(".env", "../.env")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	var mailClient mailer.Mailer
	if cfg.ResendAPIKey != "" {
		mailClient = mailer.NewResendMailer(cfg.ResendAPIKey, cfg.EmailFrom)
	} else {
		mailClient = mailer.NewLogMailer(logger)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewSendEmailWorker(mailClient))
	river.AddWorker(workers, jobs.NewSweepSoftDeletedWorkspacesWorker(pool, 30*24*time.Hour))
	jobClient, err := jobs.NewClient(pool, workers, jobs.Config{
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}},
	})
	if err != nil {
		logger.Error("jobs client failed", "err", err)
		os.Exit(1)
	}
	if err := jobClient.Start(ctx); err != nil {
		logger.Error("jobs start failed", "err", err)
		os.Exit(1)
	}

	handler := folioHTTP.NewRouter(folioHTTP.Deps{
		Logger: logger,
		DB:     pool,
		Cfg:    cfg,
		Mailer: mailClient,
		Jobs:   jobClient,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := jobClient.Stop(shutdownCtx); err != nil {
		logger.Error("jobs shutdown failed", "err", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "err", err)
	}
}

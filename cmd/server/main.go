package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"clicd-billing/internal/app"
	"clicd-billing/internal/automation"
	"clicd-billing/internal/billing"
	"clicd-billing/internal/config"
	"clicd-billing/internal/notifications"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := postgres.Migrate(ctx, db); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	secretBox, err := security.NewSecretBox(cfg.EncryptionKey)
	if err != nil {
		logger.Error("initialize encryption", "error", err)
		os.Exit(1)
	}
	provisioningStore := postgres.NewProvisioningStore(db)
	catalogStore := postgres.NewCatalogStore(db)
	hostname, _ := os.Hostname()
	workerID := hostname + ":" + fmt.Sprint(os.Getpid())
	worker := automation.NewWorker(provisioningStore, secretBox, logger, workerID, cfg.WorkerPollInterval)
	reconciler := automation.NewReconciler(provisioningStore, catalogStore, secretBox, logger, cfg.ReconcileInterval)
	go worker.Run(ctx)
	go reconciler.Run(ctx)
	lifecycleWorker := billing.NewWorker(postgres.NewLifecycleStore(db), logger, cfg.LifecycleInterval, cfg.RenewalLeadTime, cfg.OverdueGracePeriod, cfg.TerminationRetention)
	go lifecycleWorker.Run(ctx)
	if cfg.NotificationWebhookURL != "" {
		notificationWorker := notifications.NewWorker(postgres.NewOutboxStore(db), logger, workerID+":notifications", cfg.NotificationWebhookURL, cfg.NotificationWebhookSecret, cfg.WorkerPollInterval)
		go notificationWorker.Run(ctx)
	}

	handler, err := app.NewHandler(app.Dependencies{
		Config: cfg,
		DB:     db,
		Logger: logger,
	})
	if err != nil {
		logger.Error("initialize application", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		logger.Info("api server started", "addr", cfg.HTTPAddr, "environment", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api server stopped unexpectedly", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

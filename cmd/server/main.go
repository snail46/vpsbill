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

	"vpsbill/internal/app"
	"vpsbill/internal/automation"
	"vpsbill/internal/billing"
	"vpsbill/internal/config"
	"vpsbill/internal/notifications"
	_ "vpsbill/internal/provider/clicd"  // registers the CLICD node driver
	_ "vpsbill/internal/provider/lxdapi" // registers the LXDAPI node driver
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
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
	runtime, err := settings.NewManager(ctx, db, secretBox, cfg)
	if err != nil {
		logger.Error("load runtime settings", "error", err)
		os.Exit(1)
	}
	provisioningStore := postgres.NewProvisioningStore(db)
	catalogStore := postgres.NewCatalogStore(db)
	hostname, _ := os.Hostname()
	workerID := hostname + ":" + fmt.Sprint(os.Getpid())
	worker := automation.NewDynamicWorker(provisioningStore, secretBox, logger, workerID, func() time.Duration { return runtime.Current().WorkerPollInterval })
	reconciler := automation.NewDynamicReconciler(provisioningStore, catalogStore, secretBox, logger, func() time.Duration { return runtime.Current().ReconcileInterval })
	go worker.Run(ctx)
	go reconciler.Run(ctx)
	lifecycleWorker := billing.NewDynamicWorker(postgres.NewLifecycleStore(db), logger, func() (time.Duration, time.Duration, time.Duration, time.Duration) {
		current := runtime.Current()
		return current.LifecycleInterval, current.RenewalLeadTime, current.OverdueGracePeriod, current.TerminationRetention
	})
	go lifecycleWorker.Run(ctx)
	notificationWorker := notifications.NewDynamicWorker(postgres.NewOutboxStore(db), logger, workerID+":notifications", func() (string, string, time.Duration) {
		current := runtime.Current()
		return current.NotificationWebhookURL, current.NotificationWebhookSecret, current.WorkerPollInterval
	})
	go notificationWorker.Run(ctx)

	handler, err := app.NewHandler(app.Dependencies{
		Config:   cfg,
		DB:       db,
		Logger:   logger,
		Settings: runtime,
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

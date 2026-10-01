package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"vpsbill/internal/app"
	"vpsbill/internal/automation"
	"vpsbill/internal/backup"
	"vpsbill/internal/billing"
	"vpsbill/internal/chat"
	"vpsbill/internal/config"
	"vpsbill/internal/hatch/gateway"
	"vpsbill/internal/marketplace"
	"vpsbill/internal/notifications"
	"vpsbill/internal/notify"
	_ "vpsbill/internal/provider/clicd" // registers the CLICD node driver
	hatchprovider "vpsbill/internal/provider/hatch"
	_ "vpsbill/internal/provider/lxdapi" // registers the LXDAPI node driver
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// version is the commit the image was built from (see Dockerfile).
var version = "dev"

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
	// Background work runs on its own context so a restore can stop it
	// before replacing the data it works on.
	workCtx, stopWork := context.WithCancel(ctx)
	defer stopWork()
	provisioningStore := postgres.NewProvisioningStore(db)
	catalogStore := postgres.NewCatalogStore(db)
	hostname, _ := os.Hostname()
	workerID := hostname + ":" + fmt.Sprint(os.Getpid())
	agentHub := gateway.NewHub(logger, catalogStore.NodeExistsByEndpoint)
	hatchprovider.Register(agentHub)
	agentHub.SetEnroller(app.AgentEnroller(catalogStore, secretBox, logger))
	// Agents follow the build bundled next to this server, when there is one.
	if cfg.AgentDownloadDir != "" {
		if _, err := os.Stat(filepath.Join(cfg.AgentDownloadDir, "SHA256SUMS")); err == nil {
			agentHub.SetAgentRelease(version)
		}
	}
	var agentInternal http.Handler
	if cfg.InternalURL != "" {
		internalURL, err := cfg.ResolveInternalURL()
		if err != nil {
			logger.Error("resolve INTERNAL_URL", "error", err)
			os.Exit(1)
		}
		agentHub.EnableCluster(&gateway.Cluster{
			InstanceID: workerID, InternalURL: internalURL,
			Key: gateway.ClusterKey(cfg.SessionSecret + cfg.EncryptionKey), Directory: postgres.NewAgentDirectory(db),
		})
		agentInternal = agentHub.InternalHandler()
		logger.Info("agent forwarding enabled", "internal_url", internalURL)
	}
	worker := automation.NewDynamicWorker(provisioningStore, secretBox, logger, workerID, func() time.Duration { return runtime.Current().WorkerPollInterval })
	reconciler := automation.NewDynamicReconciler(provisioningStore, catalogStore, secretBox, logger, func() time.Duration { return runtime.Current().ReconcileInterval })
	go worker.Run(workCtx)
	agentHub.SetOnConnect(reconciler.Kick)
	go reconciler.Run(workCtx)
	lifecycleWorker := billing.NewDynamicWorker(postgres.NewLifecycleStore(db), logger, func() (time.Duration, time.Duration, time.Duration, time.Duration) {
		current := runtime.Current()
		return current.LifecycleInterval, current.RenewalLeadTime, current.OverdueGracePeriod, current.TerminationRetention
	})
	go lifecycleWorker.Run(workCtx)
	notificationWorker := notifications.NewDynamicWorker(postgres.NewOutboxStore(db), logger, workerID+":notifications", func() (string, string, time.Duration) {
		current := runtime.Current()
		return current.NotificationWebhookURL, current.NotificationWebhookSecret, current.WorkerPollInterval
	})
	go notificationWorker.Run(workCtx)
	mailNotifier := notify.New(postgres.NewMailStore(db), runtime, secretBox, logger)
	go mailNotifier.RunSender(workCtx)
	go mailNotifier.RunScanner(workCtx)
	marketStore := postgres.NewMarketplaceStore(db)
	// Escrow release and clearance are idempotent, so every replica may run it.
	marketService := marketplace.New(marketStore, catalogStore, secretBox, runtime, mailNotifier, logger)
	go marketService.Run(workCtx)
	chatHub := chat.NewHub(db, marketStore, logger)
	go chatHub.Run(workCtx)

	backups := backup.New(db, cfg.DatabaseURL, cfg.BackupDir, secretBox, cfg.EncryptionKey, version, logger)
	backups.SetHooks(backup.Hooks{
		StopWork: stopWork,
		// Docker (restart: unless-stopped) or systemd starts it again on
		// the restored data.
		Exit: func() {
			logger.Info("restarting after restore")
			os.Exit(0)
		},
	})
	go backups.RunScheduler(workCtx)

	handler, err := app.NewHandler(app.Dependencies{
		Config:        cfg,
		DB:            db,
		Logger:        logger,
		Settings:      runtime,
		AgentGateway:  agentHub,
		AgentInternal: agentInternal,
		Notifier:      mailNotifier,
		Marketplace:   marketService,
		ChatHub:       chatHub,
		Backups:       backups,
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
		logger.Info("api server started", "addr", cfg.HTTPAddr, "environment", cfg.Environment, "version", version)
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

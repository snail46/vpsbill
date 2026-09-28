package automation

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

type Reconciler struct {
	store           *postgres.ProvisioningStore
	catalog         *postgres.CatalogStore
	box             *security.SecretBox
	logger          *slog.Logger
	interval        time.Duration
	intervalCurrent func() time.Duration
}

func NewDynamicReconciler(store *postgres.ProvisioningStore, catalog *postgres.CatalogStore, box *security.SecretBox, logger *slog.Logger, interval func() time.Duration) *Reconciler {
	return &Reconciler{store: store, catalog: catalog, box: box, logger: logger, intervalCurrent: interval}
}

func NewReconciler(store *postgres.ProvisioningStore, catalog *postgres.CatalogStore, box *security.SecretBox, logger *slog.Logger, interval time.Duration) *Reconciler {
	return &Reconciler{store: store, catalog: catalog, box: box, logger: logger, interval: interval}
}

func (r *Reconciler) Run(ctx context.Context) {
	r.reconcile(ctx)
	for {
		interval := r.interval
		if r.intervalCurrent != nil {
			interval = r.intervalCurrent()
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	r.reconcileNodes(ctx)
	targets, err := r.store.ListReconcileTargets(ctx)
	if err != nil {
		r.logger.Error("list service reconciliation targets", "error", err)
		return
	}
	for _, target := range targets {
		driver, err := provider.OpenSealed(r.box, target.Sealed(), 20*time.Second)
		if err != nil {
			r.recordError(ctx, target, "error", err)
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		instance, err := driver.GetInstance(requestCtx, target.InstanceName)
		cancel()
		if errors.Is(err, provider.ErrNotFound) {
			r.recordError(ctx, target, "missing", errors.New("instance is missing on the node"))
			continue
		}
		if err != nil {
			r.recordError(ctx, target, "unknown", err)
			continue
		}
		if err := r.store.UpdateReconciledService(ctx, target.ServiceID, provider.NormalizeStatus(instance.Status), instance.ExternalID, instance.UUID, instance.IP, instance.IPv6, ""); err != nil {
			r.logger.Error("persist service reconciliation", "service_id", target.ServiceID, "error", err)
		}
	}
}

func (r *Reconciler) reconcileNodes(ctx context.Context) {
	nodes, err := r.catalog.ListNodeSecrets(ctx)
	if err != nil {
		r.logger.Error("list nodes for reconciliation", "error", err)
		return
	}
	for _, node := range nodes {
		// Cleared hosted nodes are kept for history only.
		if node.RetiredAt != nil {
			continue
		}
		if _, registered := provider.Lookup(node.ProviderType); !registered {
			continue
		}
		driver, err := provider.OpenSealed(r.box, node.Sealed(), 15*time.Second)
		if err != nil {
			_ = r.catalog.UpdateNodeHealth(ctx, node.ID, "offline", map[string]any{})
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		info, err := driver.HostInfo(requestCtx)
		cancel()
		if err != nil {
			_ = r.catalog.UpdateNodeHealth(ctx, node.ID, "offline", map[string]any{})
			continue
		}
		if err := r.catalog.RecordNodeReport(ctx, node.ID, info); err != nil {
			r.logger.Error("update node capacity", "node_id", node.ID, "error", err)
		}
	}
}

func (r *Reconciler) recordError(ctx context.Context, target postgres.ReconcileTarget, status string, cause error) {
	if err := r.store.UpdateReconciledService(ctx, target.ServiceID, status, "", "", "", "", cause.Error()); err != nil {
		r.logger.Error("persist reconciliation error", "service_id", target.ServiceID, "error", err)
	}
}

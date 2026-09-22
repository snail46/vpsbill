package automation

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"clicd-billing/internal/clicd"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"
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
		apiKey, err := r.box.Open(target.APIKeyCiphertext)
		if err != nil {
			r.recordError(ctx, target, "error", err)
			continue
		}
		client, err := clicd.NewClient(target.BaseURL, apiKey, 20*time.Second)
		if err != nil {
			r.recordError(ctx, target, "error", err)
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		container, err := client.GetContainer(requestCtx, target.InstanceName)
		cancel()
		if clicd.IsNotFound(err) {
			r.recordError(ctx, target, "missing", errors.New("instance is missing on CLICD node"))
			continue
		}
		if err != nil {
			r.recordError(ctx, target, "unknown", err)
			continue
		}
		if err := r.store.UpdateReconciledService(ctx, target.ServiceID, normalizeRuntimeStatus(container.Status), strconv.Itoa(container.ID), container.UUID, container.IP, container.IPv6, ""); err != nil {
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
		apiKey, err := r.box.Open(node.APIKeyCiphertext)
		if err != nil {
			_ = r.catalog.UpdateNodeHealth(ctx, node.ID, "offline", map[string]any{})
			continue
		}
		client, err := clicd.NewClient(node.BaseURL, apiKey, 15*time.Second)
		if err != nil {
			_ = r.catalog.UpdateNodeHealth(ctx, node.ID, "offline", map[string]any{})
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		info, err := client.HostInfo(requestCtx)
		cancel()
		if err != nil {
			_ = r.catalog.UpdateNodeHealth(ctx, node.ID, "offline", map[string]any{})
			continue
		}
		totals := clicd.CapacityFromHostInfo(info)
		if err := r.catalog.UpdateNodeHealth(ctx, node.ID, "online", info, int64(totals.VCPU), totals.RAMMB, totals.DiskGB); err != nil {
			r.logger.Error("update node capacity", "node_id", node.ID, "error", err)
		}
	}
}

func (r *Reconciler) recordError(ctx context.Context, target postgres.ReconcileTarget, status string, cause error) {
	if err := r.store.UpdateReconciledService(ctx, target.ServiceID, status, "", "", "", "", cause.Error()); err != nil {
		r.logger.Error("persist reconciliation error", "service_id", target.ServiceID, "error", err)
	}
}

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
	// kicks carries node endpoints to check right away, for agents that
	// just connected.
	kicks chan string
}

func NewDynamicReconciler(store *postgres.ProvisioningStore, catalog *postgres.CatalogStore, box *security.SecretBox, logger *slog.Logger, interval func() time.Duration) *Reconciler {
	return &Reconciler{store: store, catalog: catalog, box: box, logger: logger, intervalCurrent: interval, kicks: make(chan string, 64)}
}

func NewReconciler(store *postgres.ProvisioningStore, catalog *postgres.CatalogStore, box *security.SecretBox, logger *slog.Logger, interval time.Duration) *Reconciler {
	return &Reconciler{store: store, catalog: catalog, box: box, logger: logger, interval: interval, kicks: make(chan string, 64)}
}

// agentGrace is how long a freshly started API waits before its first
// check: Hatch agents reconnect within seconds, and checking before they
// have would mark their nodes offline until the next round.
const agentGrace = 30 * time.Second

// Kick asks for the node with this endpoint, and its services, to be
// checked now instead of at the next round: an agent that reconnects
// after an upgrade shows as online again within seconds.
func (r *Reconciler) Kick(endpoint string) {
	select {
	case r.kicks <- endpoint:
	default:
	}
}

func (r *Reconciler) Run(ctx context.Context) {
	timer := time.NewTimer(agentGrace)
	defer func() { timer.Stop() }()
	for {
		select {
		case <-ctx.Done():
			return
		case endpoint := <-r.kicks:
			r.reconcile(ctx, endpoint)
		case <-timer.C:
			r.reconcile(ctx, "")
			interval := r.interval
			if r.intervalCurrent != nil {
				interval = r.intervalCurrent()
			}
			timer = time.NewTimer(interval)
		}
	}
}

// reconcile checks the node with this endpoint and its services, or every
// node and service when endpoint is "".
func (r *Reconciler) reconcile(ctx context.Context, endpoint string) {
	r.reconcileNodes(ctx, endpoint)
	targets, err := r.store.ListReconcileTargets(ctx)
	if err != nil {
		r.logger.Error("list service reconciliation targets", "error", err)
		return
	}
	for _, target := range targets {
		if endpoint != "" && target.BaseURL != endpoint {
			continue
		}
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
			// The agent restarting for an upgrade keeps the last state
			// instead of flashing "unknown"; it is checked again when it
			// reconnects.
			if !provider.Reconnecting(driver) {
				r.recordError(ctx, target, "unknown", err)
			}
			continue
		}
		if err := r.store.UpdateReconciledService(ctx, target.ServiceID, provider.NormalizeStatus(instance.Status), instance.ExternalID, instance.UUID, instance.IP, instance.IPv6, ""); err != nil {
			r.logger.Error("persist service reconciliation", "service_id", target.ServiceID, "error", err)
		}
	}
}

func (r *Reconciler) reconcileNodes(ctx context.Context, endpoint string) {
	nodes, err := r.catalog.ListNodeSecrets(ctx)
	if err != nil {
		r.logger.Error("list nodes for reconciliation", "error", err)
		return
	}
	for _, node := range nodes {
		// Cleared hosted nodes are kept for history only.
		if node.RetiredAt != nil || (endpoint != "" && node.BaseURL != endpoint) {
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
			if !provider.Reconnecting(driver) {
				_ = r.catalog.UpdateNodeHealth(ctx, node.ID, "offline", map[string]any{})
			}
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

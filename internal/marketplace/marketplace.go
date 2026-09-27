// Package marketplace runs the hosting center's background duties: it pays
// hosts their daily share of escrowed sales, warns hosts whose nodes went
// offline and clears nodes that stay offline past the configured limit.
package marketplace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"vpsbill/internal/notify"
	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// offlineWarning is how long a node may be unreachable before its host is
// told; clearance follows after the configured number of hours.
const offlineWarning = time.Hour

type Service struct {
	store    *postgres.MarketplaceStore
	catalog  *postgres.CatalogStore
	box      *security.SecretBox
	settings *settings.Manager
	notifier *notify.Notifier
	logger   *slog.Logger
	now      func() time.Time
}

func New(store *postgres.MarketplaceStore, catalog *postgres.CatalogStore, box *security.SecretBox, runtime *settings.Manager, notifier *notify.Notifier, logger *slog.Logger) *Service {
	return &Service{store: store, catalog: catalog, box: box, settings: runtime, notifier: notifier, logger: logger, now: time.Now}
}

// Clear retires a hosted node, settles its instances and notifies everyone.
// When the node is still reachable (a host retiring it), the instances are
// deleted from it as well.
func (s *Service) Clear(ctx context.Context, nodeID string, multiplier int, reason, actorType, actorID string) (postgres.ClearanceResult, error) {
	result, err := s.store.ClearNode(ctx, nodeID, multiplier, reason, actorType, actorID)
	if err != nil {
		return result, err
	}
	s.logger.Warn("hosted node cleared", "node_id", nodeID, "multiplier", multiplier, "services", len(result.Services), "refund_minor", result.RefundMinor)
	if s.notifier != nil {
		s.notifier.NodeCleared(context.WithoutCancel(ctx), result)
	}
	go s.deleteInstances(result)
	return result, nil
}

func (s *Service) deleteInstances(result postgres.ClearanceResult) {
	if len(result.Services) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	node, err := s.catalog.NodeSecret(ctx, result.NodeID)
	if err != nil {
		return
	}
	driver, err := provider.OpenSealed(s.box, node.Sealed(), 30*time.Second)
	if err != nil {
		return
	}
	for _, item := range result.Services {
		requestCtx, cancelRequest := context.WithTimeout(ctx, 60*time.Second)
		if err := driver.DeleteInstance(requestCtx, item.InstanceName); err != nil {
			s.logger.Info("cleared instance left on node", "node_id", result.NodeID, "instance", item.InstanceName, "error", err)
		}
		cancelRequest()
	}
}

// Run releases escrow and watches hosted nodes every ten minutes.
func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.Tick(ctx)
			timer.Reset(10 * time.Minute)
		}
	}
}

func (s *Service) Tick(ctx context.Context) {
	if released, err := s.store.ReleaseEscrows(ctx, s.now()); err != nil {
		s.logger.Error("release hosting escrow", "error", err)
	} else if released > 0 {
		s.logger.Info("released hosting escrow", "escrows", released)
	}
	s.watchOffline(ctx)
}

func (s *Service) watchOffline(ctx context.Context) {
	limit := time.Duration(s.settings.Current().Marketplace.OfflineHours) * time.Hour
	if limit <= 0 {
		limit = 24 * time.Hour
	}
	nodes, err := s.store.OfflineHostedNodes(ctx, offlineWarning)
	if err != nil {
		s.logger.Error("list offline hosted nodes", "error", err)
		return
	}
	now := s.now()
	for _, node := range nodes {
		offlineFor := now.Sub(node.LastSeenAt)
		held := node.HoldUntil != nil && node.HoldUntil.After(now)
		if offlineFor >= limit && !held {
			reason := fmt.Sprintf("母机离线超过 %d 小时，系统自动清退", int(limit.Hours()))
			if _, err := s.Clear(ctx, node.ID, 2, reason, "system", ""); err != nil {
				s.logger.Error("clear offline hosted node", "node_id", node.ID, "error", err)
			}
			continue
		}
		// One warning per offline episode: a node that came back and went
		// down again has a newer last_seen_at than the last warning.
		if node.OfflineNotifiedAt == nil || node.OfflineNotifiedAt.Before(node.LastSeenAt) {
			if s.notifier != nil {
				s.notifier.HostNodeOffline(ctx, node, limit)
			}
			if err := s.store.MarkOfflineNotified(ctx, node.ID); err != nil {
				s.logger.Error("mark offline warning", "node_id", node.ID, "error", err)
			}
		}
	}
}

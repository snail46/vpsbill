package billing

import (
	"context"
	"log/slog"
	"time"

	"clicd-billing/internal/store/postgres"
)

type Worker struct {
	store                            *postgres.LifecycleStore
	logger                           *slog.Logger
	interval, lead, grace, retention time.Duration
}

func NewWorker(store *postgres.LifecycleStore, logger *slog.Logger, interval, lead, grace, retention time.Duration) *Worker {
	return &Worker{store: store, logger: logger, interval: interval, lead: lead, grace: grace, retention: retention}
}
func (w *Worker) Run(ctx context.Context) {
	w.run(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}
func (w *Worker) run(ctx context.Context) {
	result, err := w.store.Run(ctx, w.lead, w.grace, w.retention)
	if err != nil {
		w.logger.Error("billing lifecycle run failed", "error", err)
		return
	}
	if result.RenewalInvoices+result.PricingReview+result.Overdue+result.Suspended+result.TerminationQueued > 0 {
		w.logger.Info("billing lifecycle completed", "renewal_invoices", result.RenewalInvoices, "pricing_review", result.PricingReview, "overdue", result.Overdue, "suspended", result.Suspended, "termination_queued", result.TerminationQueued)
	}
}

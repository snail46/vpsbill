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
	current                          func() (time.Duration, time.Duration, time.Duration, time.Duration)
}

func NewWorker(store *postgres.LifecycleStore, logger *slog.Logger, interval, lead, grace, retention time.Duration) *Worker {
	return &Worker{store: store, logger: logger, interval: interval, lead: lead, grace: grace, retention: retention}
}
func NewDynamicWorker(store *postgres.LifecycleStore, logger *slog.Logger, current func() (time.Duration, time.Duration, time.Duration, time.Duration)) *Worker {
	return &Worker{store: store, logger: logger, current: current}
}
func (w *Worker) Run(ctx context.Context) {
	w.run(ctx)
	for {
		interval := w.interval
		if w.current != nil {
			interval, _, _, _ = w.current()
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			w.run(ctx)
		}
	}
}
func (w *Worker) run(ctx context.Context) {
	lead, grace, retention := w.lead, w.grace, w.retention
	if w.current != nil {
		_, lead, grace, retention = w.current()
	}
	result, err := w.store.Run(ctx, lead, grace, retention)
	if err != nil {
		w.logger.Error("billing lifecycle run failed", "error", err)
		return
	}
	if result.RenewalInvoices+result.PricingReview+result.Overdue+result.Suspended+result.TerminationQueued > 0 {
		w.logger.Info("billing lifecycle completed", "renewal_invoices", result.RenewalInvoices, "pricing_review", result.PricingReview, "overdue", result.Overdue, "suspended", result.Suspended, "termination_queued", result.TerminationQueued)
	}
}

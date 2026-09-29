package app

import (
	"context"
	"fmt"
	"net/http"

	"vpsbill/internal/store/postgres"
)

// checkPlanStock refuses a stock above what the plan's nodes can hold.
// Keeping or lowering a saved stock is always allowed, so a smaller oversell
// ratio does not block unrelated edits. It returns a message for the user.
func checkPlanStock(ctx context.Context, catalog *postgres.CatalogStore, plan postgres.Plan, existingID string) (string, error) {
	if plan.StockLimit == nil {
		return "", nil
	}
	if *plan.StockLimit < 0 || *plan.StockLimit > 100000 {
		return "库存需在 0-100000 之间", nil
	}
	if existingID != "" {
		current, err := catalog.PlanStockLimit(ctx, existingID)
		if err != nil {
			return "", err
		}
		if current != nil && *plan.StockLimit <= *current {
			return "", nil
		}
	}
	capacity, err := catalog.PlanStockCapacity(ctx, plan, existingID)
	if err != nil {
		return "", err
	}
	if *plan.StockLimit > capacity.Max {
		return fmt.Sprintf("库存最多 %d 台（按母机配置、超售倍数和其他套餐占用计算）", capacity.Max), nil
	}
	return "", nil
}

const diskIOInvalid = "磁盘读写上限无效：MB/s 需在 0-100000 之间，IOPS 需在 0-10000000 之间（0 表示不限）"

// validatePriceLimits checks the per-price purchase limits.
func validatePriceLimits(prices []postgres.Price) string {
	for _, price := range prices {
		if price.PurchaseLimit != nil && (*price.PurchaseLimit < 1 || *price.PurchaseLimit > 1000000) {
			return "限购次数需在 1-1000000 之间，留空表示不限"
		}
	}
	return ""
}

// writeStockCapacity answers a stock preview for a plan draft.
func writeStockCapacity(w http.ResponseWriter, r *http.Request, catalog *postgres.CatalogStore, plan postgres.Plan, existingID string) {
	capacity, err := catalog.PlanStockCapacity(r.Context(), plan, existingID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": capacity})
}

// Package fx keeps the USD display rate current. The ledger is in CNY; the
// rate only changes what the portal shows to visitors who chose USD.
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"vpsbill/internal/settings"
)

const (
	updateInterval = 6 * time.Hour
	firstWait      = 30 * time.Second
)

// Sources are public rate services that need no API key; the first that
// answers wins.
var Sources = []string{
	"https://open.er-api.com/v6/latest/USD",
	"https://api.frankfurter.dev/v1/latest?base=USD&symbols=CNY",
}

// Fetch reads how many CNY one USD is worth.
func Fetch(ctx context.Context, client *http.Client) (float64, error) {
	last := errors.New("no rate source")
	for _, source := range Sources {
		rate, err := fetchFrom(ctx, client, source)
		if err == nil {
			return rate, nil
		}
		last = err
	}
	return 0, last
}

func fetchFrom(ctx context.Context, client *http.Client, source string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
	}
	var payload struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return 0, err
	}
	rate := payload.Rates["CNY"]
	if rate <= 0 {
		return 0, errors.New("no CNY rate in the answer")
	}
	return rate, nil
}

// Run updates the rate while automatic updates are on, until ctx ends.
func Run(ctx context.Context, runtime *settings.Manager, logger *slog.Logger) {
	client := &http.Client{Timeout: 20 * time.Second}
	wait := firstWait
	for {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		wait = updateInterval
		current := runtime.Current()
		if !current.Installed || !current.Locale.USDRateAuto || (!current.Locale.USDEnabled && current.Telegram.Currency != "USD") {
			continue
		}
		rate, err := Fetch(ctx, client)
		if err == nil {
			err = runtime.RecordUSDRate(ctx, rate)
		}
		if err != nil {
			logger.Warn("update usd rate", "error", err)
		}
	}
}

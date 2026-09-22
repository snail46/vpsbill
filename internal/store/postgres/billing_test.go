package postgres

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeOrderConfigurationRejectsResourceOverride(t *testing.T) {
	_, err := sanitizeOrderConfiguration(map[string]any{"template_id": "debian", "vcpu": float64(64)})
	if err == nil {
		t.Fatal("expected resource override to be rejected")
	}
}

func TestSanitizeOrderConfigurationAllowsTemplate(t *testing.T) {
	got, err := sanitizeOrderConfiguration(map[string]any{"template_id": "debian-bookworm"})
	if err != nil {
		t.Fatal(err)
	}
	if got["template_id"] != "debian-bookworm" {
		t.Fatalf("unexpected configuration: %#v", got)
	}
}

func TestSanitizeOrderConfigurationRejectsNetworkOverride(t *testing.T) {
	if _, err := sanitizeOrderConfiguration(map[string]any{"template_id": "debian-bookworm", "assign_nat": true}); err == nil {
		t.Fatal("expected customer network override to be rejected")
	}
}

func TestAddBillingCycle(t *testing.T) {
	base := time.Date(2026, time.January, 15, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		cycle string
		want  time.Time
	}{
		{"monthly", time.Date(2026, time.February, 15, 10, 0, 0, 0, time.UTC)},
		{"quarterly", time.Date(2026, time.April, 15, 10, 0, 0, 0, time.UTC)},
		{"semiannual", time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)},
		{"annual", time.Date(2027, time.January, 15, 10, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		if got := addBillingCycle(base, test.cycle); !got.Equal(test.want) {
			t.Fatalf("cycle %s: got %s want %s", test.cycle, got, test.want)
		}
	}
}

func TestAddBillingCycleClampsMonthEnd(t *testing.T) {
	january := time.Date(2027, time.January, 31, 8, 0, 0, 0, time.UTC)
	want := time.Date(2027, time.February, 28, 8, 0, 0, 0, time.UTC)
	if got := addBillingCycle(january, "monthly"); !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
	leap := time.Date(2028, time.February, 29, 8, 0, 0, 0, time.UTC)
	wantAnnual := time.Date(2029, time.February, 28, 8, 0, 0, 0, time.UTC)
	if got := addBillingCycle(leap, "annual"); !got.Equal(wantAnnual) {
		t.Fatalf("got %s want %s", got, wantAnnual)
	}
}

func TestDocumentNumber(t *testing.T) {
	number := newDocumentNumber("INV")
	if !strings.HasPrefix(number, "INV-") || len(number) < 20 {
		t.Fatalf("unexpected document number: %s", number)
	}
}

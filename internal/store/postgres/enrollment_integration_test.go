package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestEnrollmentIntegration covers enroll keys, the pending agent list and
// regions created by name.
func TestEnrollmentIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	if !strings.Contains(databaseURL, "vpsbill_test") {
		t.Fatal("refusing to reset database without vpsbill_test in TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	billing := NewBillingStore(db)
	catalog := NewCatalogStore(db)
	host, err := billing.CreateAccount(ctx, Account{Kind: "individual", DisplayName: "Host", BillingEmail: "host@example.com", CountryCode: "CN", DefaultCurrency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := billing.CreateAccount(ctx, Account{Kind: "individual", DisplayName: "Other", BillingEmail: "other@example.com", CountryCode: "CN", DefaultCurrency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}

	key, err := catalog.EnrollKey(ctx, host.ID)
	if again, _ := catalog.EnrollKey(ctx, host.ID); err != nil || len(key) != 32 || again != key {
		t.Fatalf("enroll key %q then %q, err=%v", key, again, err)
	}
	if ok, err := catalog.Enroll(ctx, "not-a-key-000000", "agent://aa", []byte("x"), "h", "v", nil, "203.0.113.1"); ok || err != nil {
		t.Fatalf("unknown key accepted: %v %v", ok, err)
	}
	if ok, err := catalog.Enroll(ctx, key, "agent://aa", []byte("sealed"), "hk-01", "1.0", []string{"podman"}, "203.0.113.1"); !ok || err != nil {
		t.Fatalf("enroll: %v %v", ok, err)
	}
	// Reconnecting refreshes the row instead of adding another.
	if ok, _ := catalog.Enroll(ctx, key, "agent://aa", []byte("sealed2"), "hk-01b", "1.1", []string{"podman", "lxc"}, "203.0.113.2"); !ok {
		t.Fatal("reconnect refused")
	}
	pending, err := catalog.PendingAgents(ctx, host.ID)
	if err != nil || len(pending) != 1 || pending[0].Hostname != "hk-01b" || len(pending[0].Runtimes) != 2 {
		t.Fatalf("pending = %+v err=%v", pending, err)
	}
	if list, _ := catalog.PendingAgents(ctx, other.ID); len(list) != 0 {
		t.Fatalf("other account sees %+v", list)
	}
	if _, err := catalog.EnrollmentToken(ctx, pending[0].ID, other.ID); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Fatalf("other account opened the token: %v", err)
	}
	if sealed, err := catalog.EnrollmentToken(ctx, pending[0].ID, host.ID); err != nil || string(sealed) != "sealed2" {
		t.Fatalf("token %q err=%v", sealed, err)
	}
	// The owner's cap on waiting agents.
	for i := 1; i < maxPendingAgents; i++ {
		if ok, _ := catalog.Enroll(ctx, key, "agent://f"+strings.Repeat("0", i), []byte("x"), "", "", nil, ""); !ok {
			t.Fatalf("agent %d refused below the cap", i)
		}
	}
	if ok, _ := catalog.Enroll(ctx, key, "agent://over", []byte("x"), "", "", nil, ""); ok {
		t.Fatal("agent over the cap accepted")
	}
	if err := catalog.DismissEnrollment(ctx, pending[0].ID, other.ID); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Fatalf("other account dismissed: %v", err)
	}
	if err := catalog.DismissEnrollment(ctx, pending[0].ID, host.ID); err != nil {
		t.Fatal(err)
	}

	// Regions by name: created once, matched ignoring case, refused when
	// disabled.
	hongKong, err := catalog.EnsureRegion(ctx, "Hong Kong")
	if err != nil || hongKong.Code != "HONGKONG" {
		t.Fatalf("region %+v err=%v", hongKong, err)
	}
	if same, err := catalog.EnsureRegion(ctx, " hong kong "); err != nil || same.ID != hongKong.ID {
		t.Fatalf("second lookup %+v err=%v", same, err)
	}
	tokyo, err := catalog.EnsureRegion(ctx, "东京")
	if err != nil || !strings.HasPrefix(tokyo.Code, "R") || tokyo.Name != "东京" {
		t.Fatalf("CJK region %+v err=%v", tokyo, err)
	}
	if err := catalog.UpdateRegion(ctx, tokyo.ID, "日本东京", false); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.EnsureRegion(ctx, "日本东京"); !errors.Is(err, ErrRegionDisabled) {
		t.Fatalf("disabled region used: %v", err)
	}
	all, err := catalog.AllRegions(ctx)
	if err != nil || len(all) != 2 || !all[0].Enabled {
		t.Fatalf("regions %+v err=%v", all, err)
	}
}

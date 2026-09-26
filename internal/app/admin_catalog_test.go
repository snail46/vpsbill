package app

import (
	"testing"

	_ "vpsbill/internal/provider/clicd"
	"vpsbill/internal/store/postgres"
)

func TestValidatePlanRequiresNetworkPolicy(t *testing.T) {
	plan := postgres.Plan{
		Code: "LXC-TEST", Name: "Test", ProviderType: "clicd", Virtualization: "lxc", VCPU: 1, RAMMB: 512, DiskGB: 10,
		IPv4Count: 1, IPv6Count: 1, DefaultTemplateID: "debian-bookworm",
		AllowedTemplateIDs: []string{"debian-bookworm"},
		Prices:             []postgres.Price{{Currency: "CNY", BillingCycle: "monthly", AmountMinor: 100}},
	}
	if got := validatePlan(plan); got != "套餐至少需要启用一种网络方式" {
		t.Fatalf("unexpected validation result: %q", got)
	}
	plan.AssignNAT = true
	if got := validatePlan(plan); got != "" {
		t.Fatalf("valid plan rejected: %q", got)
	}
}

package automation

import (
	"clicd-billing/internal/store/postgres"
	"testing"
)

func TestBuildCreateSpec(t *testing.T) {
	spec := buildCreateSpec(postgres.ProvisionContext{InstanceName: "svc-1", Virtualization: "lxc", VCPU: 2, RAMMB: 2048, DiskGB: 20, Configuration: map[string]any{"template_id": "debian", "assign_nat": true, "assign_ipv6": true}})
	if spec.Name != "svc-1" || spec.TemplateID != "debian" || !spec.AssignNAT || !spec.AssignIPv6 || spec.AssignIPv4 {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	if spec.SSHAuthMode != "auto_password" {
		t.Fatalf("expected auto-generated SSH password, got %q", spec.SSHAuthMode)
	}
}

func TestNormalizeRuntimeStatus(t *testing.T) {
	if normalizeRuntimeStatus("started") != "running" || normalizeRuntimeStatus("surprise") != "unknown" {
		t.Fatal("runtime status normalization failed")
	}
}

func TestBuildCreateSpecHonorsPlanNetworkAndRepairsLegacyPasswordMode(t *testing.T) {
	spec := buildCreateSpec(postgres.ProvisionContext{Configuration: map[string]any{
		"assign_nat": true, "assign_ipv4": true, "ssh_auth_mode": "password",
	}})
	if !spec.AssignNAT || !spec.AssignIPv4 {
		t.Fatalf("expected both plan network modes, got %+v", spec)
	}
	if spec.SSHAuthMode != "auto_password" {
		t.Fatalf("missing legacy custom password should fall back to auto_password, got %q", spec.SSHAuthMode)
	}
}

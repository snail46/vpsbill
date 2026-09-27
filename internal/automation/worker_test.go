package automation

import (
	"context"
	"testing"

	"vpsbill/internal/provider"
	"vpsbill/internal/store/postgres"
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

type fakeDriver struct {
	status string
	calls  []string
}

func (d *fakeDriver) HostInfo(context.Context) (provider.HostInfo, error) {
	return provider.HostInfo{}, nil
}
func (d *fakeDriver) Images(context.Context) ([]provider.Image, error) { return nil, nil }
func (d *fakeDriver) EnsureInstance(context.Context, provider.CreateSpec) (provider.EnsureResult, error) {
	return provider.EnsureResult{}, nil
}
func (d *fakeDriver) GetInstance(context.Context, string) (provider.Instance, error) {
	return provider.Instance{Name: "svc", Status: d.status}, nil
}
func (d *fakeDriver) PowerAction(_ context.Context, _ string, action string) (string, error) {
	d.calls = append(d.calls, action)
	return "task", nil
}
func (d *fakeDriver) DeleteInstance(context.Context, string) error { return nil }

type suspendingDriver struct{ fakeDriver }

func (d *suspendingDriver) Suspend(context.Context, string) error {
	d.calls = append(d.calls, "suspend")
	return nil
}
func (d *suspendingDriver) Resume(context.Context, string) error {
	d.calls = append(d.calls, "resume")
	return nil
}

func TestOverdueStopPausesWhenSupported(t *testing.T) {
	ctx := context.Background()
	plain := &fakeDriver{}
	if _, desired, _ := powerAction(ctx, plain, "svc", "stop", "billing_lifecycle"); desired != "" || len(plain.calls) != 1 || plain.calls[0] != "stop" {
		t.Fatalf("driver without suspend must power off: %v %q", plain.calls, desired)
	}
	pausing := &suspendingDriver{}
	if _, desired, _ := powerAction(ctx, pausing, "svc", "stop", "billing_lifecycle"); desired != "suspended" || pausing.calls[0] != "suspend" {
		t.Fatalf("overdue stop must pause: %v %q", pausing.calls, desired)
	}
	customer := &suspendingDriver{}
	if _, _, _ = powerAction(ctx, customer, "svc", "stop", ""); customer.calls[0] != "stop" {
		t.Fatalf("customer stop must power off: %v", customer.calls)
	}
	paused := &suspendingDriver{fakeDriver{status: "Frozen"}}
	if _, desired, _ := powerAction(ctx, paused, "svc", "start", "renewal_payment"); desired != "running" || paused.calls[0] != "resume" || len(paused.calls) != 1 {
		t.Fatalf("start must resume a paused instance: %v", paused.calls)
	}
	stopped := &suspendingDriver{fakeDriver{status: "stopped"}}
	if _, _, _ = powerAction(ctx, stopped, "svc", "start", "renewal_payment"); stopped.calls[0] != "start" {
		t.Fatalf("start must power on a stopped instance: %v", stopped.calls)
	}
}

func TestOffersTemplateNeedsReadyImage(t *testing.T) {
	images := []provider.Image{
		{ID: "debian-12", Enabled: true, Downloaded: true},
		{ID: "ubuntu-24", Enabled: true, Downloaded: false},
	}
	if !offersTemplate(images, "debian-12") {
		t.Fatal("ready image not offered")
	}
	if offersTemplate(images, "ubuntu-24") || offersTemplate(images, "alpine") {
		t.Fatal("missing or undownloaded image offered")
	}
}

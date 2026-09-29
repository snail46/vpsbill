package automation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

const maxProvisionAttempts = 8

type Worker struct {
	store       *postgres.ProvisioningStore
	box         *security.SecretBox
	logger      *slog.Logger
	workerID    string
	poll        time.Duration
	pollCurrent func() time.Duration
}

func NewWorker(store *postgres.ProvisioningStore, box *security.SecretBox, logger *slog.Logger, workerID string, poll time.Duration) *Worker {
	return &Worker{store: store, box: box, logger: logger, workerID: workerID, poll: poll}
}

func NewDynamicWorker(store *postgres.ProvisioningStore, box *security.SecretBox, logger *slog.Logger, workerID string, poll func() time.Duration) *Worker {
	return &Worker{store: store, box: box, logger: logger, workerID: workerID, pollCurrent: poll}
}

func (w *Worker) Run(ctx context.Context) {
	if count, err := w.store.RecoverStaleJobs(ctx, 10*time.Minute); err != nil {
		w.logger.Error("recover stale provisioning jobs", "error", err)
	} else if count > 0 {
		w.logger.Warn("recovered stale provisioning jobs", "count", count)
	}
	w.drain(ctx)
	for {
		interval := w.poll
		if w.pollCurrent != nil {
			interval = w.pollCurrent()
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			w.drain(ctx)
		}
	}
}

func (w *Worker) drain(ctx context.Context) {
	for {
		job, ok, err := w.store.ClaimJob(ctx, w.workerID)
		if err != nil {
			w.logger.Error("claim provisioning job", "error", err)
			return
		}
		if !ok {
			return
		}
		if err := w.execute(ctx, job); err != nil {
			w.logger.Warn("provisioning job failed", "job_id", job.ID, "attempt", job.Attempts, "error", err)
			if failErr := w.store.FailJob(ctx, job, err, maxProvisionAttempts); failErr != nil {
				w.logger.Error("persist provisioning failure", "job_id", job.ID, "error", failErr)
			}
		}
	}
}

func (w *Worker) execute(parent context.Context, job postgres.ProvisioningJob) error {
	switch job.Action {
	case "provision":
		return w.executeProvision(parent, job)
	case "start", "stop", "restart":
		return w.executePowerAction(parent, job)
	case "terminate":
		return w.executeTerminate(parent, job)
	default:
		return fmt.Errorf("unsupported automation action %q", job.Action)
	}
}

func (w *Worker) executeTerminate(parent context.Context, job postgres.ProvisioningJob) error {
	action, err := w.store.ActionContext(parent, job.ID)
	if err != nil {
		return fmt.Errorf("load termination context: %w", err)
	}
	driver, err := provider.OpenSealed(w.box, action.Sealed(), 45*time.Second)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	if err = driver.DeleteInstance(requestCtx, action.InstanceName); err != nil {
		return err
	}
	return w.store.CompleteTermination(parent, job.ID, w.workerID)
}

func (w *Worker) executeProvision(parent context.Context, job postgres.ProvisioningJob) error {
	excluded, err := w.nodesMissingTemplate(parent, job)
	if err != nil {
		return fmt.Errorf("schedule node: %w", err)
	}
	if _, err := w.store.ReserveNode(parent, job.ServiceID, excluded...); err != nil {
		return fmt.Errorf("schedule node: %w", err)
	}
	provision, err := w.store.ProvisionContext(parent, job.ID)
	if err != nil {
		return fmt.Errorf("load provision context: %w", err)
	}
	driver, err := provider.OpenSealed(w.box, provision.Sealed(), 90*time.Second)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	result, err := driver.EnsureInstance(requestCtx, buildCreateSpec(provision))
	if err != nil {
		return err
	}
	instance := result.Instance
	var rootPasswordCiphertext []byte
	if instance.InitialPassword != "" {
		rootPasswordCiphertext, err = w.box.Seal(instance.InitialPassword)
		if err != nil {
			return fmt.Errorf("encrypt initial root password: %w", err)
		}
	}
	return w.store.CompleteProvision(parent, job.ID, w.workerID, instance.ExternalID, instance.UUID,
		instance.IP, instance.IPv6, provider.NormalizeStatus(instance.Status), rootPasswordCiphertext)
}

// nodesMissingTemplate returns candidate nodes that answered their image list
// without the template the customer chose, so placement skips them. Nodes
// that cannot be asked stay eligible and report their own error on create.
func (w *Worker) nodesMissingTemplate(ctx context.Context, job postgres.ProvisioningJob) ([]string, error) {
	configuration, _ := job.Payload["configuration"].(map[string]any)
	templateID := stringValue(configuration, "template_id")
	if templateID == "" {
		return nil, nil
	}
	candidates, err := w.store.PlacementCandidates(ctx, job.ServiceID)
	if err != nil || len(candidates) < 2 {
		return nil, err
	}
	var missing []string
	for _, candidate := range candidates {
		driver, err := provider.OpenSealed(w.box, candidate.Sealed(), 20*time.Second)
		if err != nil {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		images, err := driver.Images(requestCtx)
		cancel()
		if err != nil {
			continue
		}
		if !offersTemplate(images, templateID) {
			missing = append(missing, candidate.NodeID)
		}
	}
	if len(missing) == len(candidates) {
		return nil, fmt.Errorf("no online node offers template %q", templateID)
	}
	return missing, nil
}

func offersTemplate(images []provider.Image, templateID string) bool {
	for _, image := range images {
		if image.ID == templateID && image.Enabled && image.Downloaded {
			return true
		}
	}
	return false
}

func (w *Worker) executePowerAction(parent context.Context, job postgres.ProvisioningJob) error {
	action, err := w.store.ActionContext(parent, job.ID)
	if err != nil {
		return fmt.Errorf("load action context: %w", err)
	}
	driver, err := provider.OpenSealed(w.box, action.Sealed(), 30*time.Second)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	taskID, desired, err := powerAction(requestCtx, driver, action.InstanceName, job.Action, action.Source)
	if err != nil {
		return err
	}
	return w.store.CompleteAction(parent, job.ID, w.workerID, taskID, desired)
}

// powerAction performs a power job. Overdue suspension pauses the instance
// when the node supports it, keeping its memory state for a quick recovery,
// and a later start resumes a paused instance before powering it on. The
// returned desired status overrides the service's target when non-empty.
func powerAction(ctx context.Context, driver provider.Driver, name, action, source string) (string, string, error) {
	suspender, canSuspend := driver.(provider.Suspender)
	switch {
	case action == "stop" && source == "billing_lifecycle" && canSuspend:
		return "", "suspended", suspender.Suspend(ctx, name)
	case action == "start" && canSuspend:
		instance, err := driver.GetInstance(ctx, name)
		if err != nil {
			return "", "", err
		}
		switch provider.NormalizeStatus(instance.Status) {
		case "suspended":
			return "", "running", suspender.Resume(ctx, name)
		case "running":
			return "", "running", nil
		}
	}
	taskID, err := driver.PowerAction(ctx, name, action)
	return taskID, "", err
}

func buildCreateSpec(value postgres.ProvisionContext) provider.CreateSpec {
	assignNAT := boolValue(value.Configuration, "assign_nat", true)
	sshMode := stringValueDefault(value.Configuration, "ssh_auth_mode", "auto_password")
	sshPassword := stringValue(value.Configuration, "ssh_password")
	if sshMode == "password" && sshPassword == "" {
		sshMode = "auto_password"
	}
	return provider.CreateSpec{
		Name: value.InstanceName, Virtualization: value.Virtualization, TemplateID: stringValue(value.Configuration, "template_id"),
		VCPU: value.VCPU, RAMMB: value.RAMMB, DiskGB: value.DiskGB, AssignNAT: assignNAT,
		PortMappingCount: intValue(value.Configuration, "port_mapping_count"), AssignIPv4: boolValue(value.Configuration, "assign_ipv4", !assignNAT),
		IPv4Count: intValueDefault(value.Configuration, "ipv4_count", 1), AssignIPv6: boolValue(value.Configuration, "assign_ipv6", false),
		IPv6Count: intValueDefault(value.Configuration, "ipv6_count", 1), SSHAuthMode: sshMode,
		SSHPassword:  sshPassword,
		SSHPublicKey: stringValue(value.Configuration, "ssh_public_key"), ExpiresAt: value.ExpiresAt,
		NetworkDownMbps: value.NetworkDownMbps, NetworkUpMbps: value.NetworkUpMbps,
		MonthlyTrafficGB: value.TrafficGB, SnapshotLimit: value.SnapshotLimit, DiskIO: value.DiskIO,
	}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
func stringValueDefault(values map[string]any, key, fallback string) string {
	if value := stringValue(values, key); value != "" {
		return value
	}
	return fallback
}
func boolValue(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key].(bool)
	if ok {
		return value
	}
	return fallback
}
func intValue(values map[string]any, key string) int {
	value, ok := values[key].(float64)
	if ok {
		return int(value)
	}
	return 0
}
func intValueDefault(values map[string]any, key string, fallback int) int {
	if value := intValue(values, key); value > 0 {
		return value
	}
	return fallback
}

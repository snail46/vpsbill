package settings

import (
	"testing"
	"time"
)

func TestValidateInstallDefaultsAndGeneratedSecrets(t *testing.T) {
	value, generated, err := validate(InstallInput{
		AppName: "VPSBill", PublicURL: "https://billing.example.com/", Timezone: "Asia/Shanghai",
		NotificationWebhookURL: "https://notify.example.com/events",
		AdminDisplayName:       "Administrator", AdminEmail: "admin@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.PublicURL != "https://billing.example.com" || value.PaymentProviderName != "generic" {
		t.Fatalf("unexpected defaults: %+v", value)
	}
	if value.WorkerPollInterval != 3*time.Second || value.ReconcileInterval != 5*time.Minute {
		t.Fatalf("unexpected intervals: %+v", value)
	}
	if len(value.PaymentWebhookSecret) < 32 || len(value.NotificationWebhookSecret) < 32 || len(value.MetricsToken) < 32 {
		t.Fatal("generated secrets are too short")
	}
	if !generated.GeneratedPaymentSecret || !generated.GeneratedNotificationSecret || !generated.GeneratedMetricsToken {
		t.Fatalf("generated flags: %+v", generated)
	}
}

func TestValidateInstallRejectsInvalidURLAndDuration(t *testing.T) {
	base := InstallInput{AppName: "VPSBill", PublicURL: "not-a-url", Timezone: "UTC", AdminDisplayName: "Admin", AdminEmail: "admin@example.com"}
	if _, _, err := validate(base); err == nil {
		t.Fatal("invalid public URL accepted")
	}
	base.PublicURL = "https://billing.example.com"
	base.WorkerPollInterval = "zero"
	if _, _, err := validate(base); err == nil {
		t.Fatal("invalid duration accepted")
	}
}

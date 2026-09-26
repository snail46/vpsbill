package app

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"vpsbill/internal/security"
	"vpsbill/internal/settings"
)

type installer struct {
	settings *settings.Manager
	auth     *authenticator
	logger   *slog.Logger
}

func (i *installer) status(w http.ResponseWriter, _ *http.Request) {
	runtime := i.settings.Current()
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"required": !runtime.Installed,
		"defaults": map[string]string{
			"app_name": "CLICD Billing", "timezone": "Asia/Shanghai",
			"worker_poll_interval": "3s",
			"reconcile_interval":   "5m", "lifecycle_interval": "1m",
			"renewal_lead_time": "168h", "overdue_grace_period": "72h",
			"termination_retention": "168h",
		},
	}})
}

func (i *installer) install(w http.ResponseWriter, r *http.Request) {
	var input settings.InstallInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validEmail(input.AdminEmail) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "管理员邮箱无效"})
		return
	}
	passwordHash, err := security.HashPassword(input.AdminPassword)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	input.AdminPasswordHash = passwordHash
	result, err := i.settings.Install(r.Context(), input)
	if errors.Is(err, settings.ErrAlreadyInstalled) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "installation_complete", "message": "系统已经完成安装"})
		return
	}
	if errors.Is(err, settings.ErrInvalidSettings) {
		message := strings.TrimPrefix(err.Error(), settings.ErrInvalidSettings.Error()+": ")
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	if err != nil {
		i.logger.Error("system installation failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "installation_failed", "message": "安装写入失败，请检查服务日志"})
		return
	}
	runtime := i.settings.Current()
	generated := map[string]string{}
	if result.GeneratedNotificationSecret {
		generated["notification_webhook_secret"] = runtime.NotificationWebhookSecret
	}
	if result.GeneratedMetricsToken {
		generated["metrics_token"] = runtime.MetricsToken
	}
	i.auth.issueSession(w, r, result.Identity.UserID, map[string]any{"user": result.Identity, "generated_secrets": generated})
}

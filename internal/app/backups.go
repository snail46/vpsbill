package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"vpsbill/internal/backup"
)

// backupAPI is the admin backup page: settings, runs, local and WebDAV
// files, and restores. Everything needs the backups:manage permission,
// which only the super administrator role has by default.
type backupAPI struct {
	service *backup.Service
	db      *pgxpool.Pool
	dir     string
}

func (a *backupAPI) audit(r *http.Request, action, target string, metadata map[string]any) {
	body, _ := json.Marshal(metadata)
	if metadata == nil {
		body = []byte("{}")
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,ip,user_agent,metadata) VALUES('staff',$1,$2,'backup',$3,nullif($4,'')::inet,$5,$6)`,
		principalFromContext(r.Context()).UserID, action, target, remoteIP(r), r.UserAgent(), body)
}

func (a *backupAPI) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backup.ErrInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": strings.TrimPrefix(err.Error(), backup.ErrInvalid.Error()+": ")})
	case errors.Is(err, backup.ErrBusy):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "busy", "message": err.Error()})
	case errors.Is(err, backup.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": err.Error()})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "backup_failed", "message": err.Error()})
	}
}

func (a *backupAPI) overview(w http.ResponseWriter, r *http.Request) {
	settings, err := a.service.View(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	files, err := a.service.ListLocal()
	if err != nil {
		files = []backup.LocalFile{}
	}
	runs, err := a.service.Runs(r.Context(), 30)
	if err != nil {
		runs = []backup.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"settings":   settings,
		"local":      files,
		"runs":       runs,
		"activity":   a.service.Activity(),
		"directory":  a.dir,
		"persistent": a.service.Persistent(),
	}})
}

func (a *backupAPI) saveSettings(w http.ResponseWriter, r *http.Request) {
	var input backup.SettingsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	view, err := a.service.Save(r.Context(), input)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "backup.settings_updated", "settings", map[string]any{"schedule": view.Schedule, "webdav": view.WebDAVURL != ""})
	writeJSON(w, http.StatusOK, map[string]any{"data": view})
}

func (a *backupAPI) testWebDAV(w http.ResponseWriter, r *http.Request) {
	var input backup.SettingsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	message, err := a.service.TestWebDAV(r.Context(), input)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"message": message}})
}

func (a *backupAPI) run(w http.ResponseWriter, r *http.Request) {
	if err := a.service.Start("manual", principalFromContext(r.Context()).UserID); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "backup.started", "manual", nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"activity": a.service.Activity()}})
}

func (a *backupAPI) remote(w http.ResponseWriter, r *http.Request) {
	files, err := a.service.ListRemote(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": files})
}

func (a *backupAPI) fetch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.service.Fetch(name, principalFromContext(r.Context()).UserID); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "backup.fetched", name, nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"activity": a.service.Activity()}})
}

func (a *backupAPI) download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := a.service.LocalPath(name)
	if err != nil {
		a.fail(w, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		a.fail(w, backup.ErrNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		a.fail(w, err)
		return
	}
	// A backup can take longer than the server's write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Hour))
	a.audit(r, "backup.downloaded", name, nil)
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (a *backupAPI) deleteLocal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.service.DeleteLocal(name); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "backup.deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// upload stores a backup file sent as the raw request body.
func (a *backupAPI) upload(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(6 * time.Hour))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Hour))
	file, err := a.service.SaveUpload(r.Body)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "backup.uploaded", file.Name, map[string]any{"bytes": file.SizeBytes})
	writeJSON(w, http.StatusCreated, map[string]any{"data": file})
}

func (a *backupAPI) restore(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm          string `json:"confirm"`
		AllowKeyMismatch bool   `json:"allow_key_mismatch"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Confirm) != "还原" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请输入「还原」确认操作"})
		return
	}
	// Checking a large backup's checksum takes a while.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Hour))
	name := r.PathValue("name")
	manifest, err := a.service.Restore(r.Context(), name, input.AllowKeyMismatch, principalFromContext(r.Context()).UserID)
	if errors.Is(err, backup.ErrKeyMismatch) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "key_mismatch", "message": "这份备份使用了另一个 ENCRYPTION_KEY：还原后，支付密钥、节点令牌、SMTP 密码和 WebDAV 密码等已保存的密钥无法解密，需要重新填写。建议先把 .env 里的 ENCRYPTION_KEY 改回备份时的值。"})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "backup.restore_started", name, map[string]any{"created_at": manifest.CreatedAt, "app_version": manifest.AppVersion})
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{"manifest": manifest, "activity": a.service.Activity()}})
}

// maintenance answers every request but health checks and the backup page
// with 503 while a restore replaces the data.
func maintenance(service *backup.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service == nil || !service.Restoring() || strings.HasPrefix(r.URL.Path, "/health/") {
			next.ServeHTTP(w, r)
			return
		}
		// The backup page polls this while the tables it would check a
		// session against are being replaced; it only learns the stage.
		if r.URL.Path == "/api/v1/admin/backups" && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"restoring": true, "activity": service.Activity()}})
			return
		}
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "maintenance", "message": "系统正在还原数据，完成后会自动重启，请稍候再试"})
	})
}

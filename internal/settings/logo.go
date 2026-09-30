package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// LogoMaxBytes caps an uploaded logo; it is shown at 36 pixels.
const LogoMaxBytes = 512 << 10

var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/x-icon": true}

// Logo is where the site logo is shown from: the uploaded image, an
// external URL, or "" for the default mark.
func (r Runtime) Logo() string {
	if r.LogoVersion != "" {
		return "/api/v1/site/logo?v=" + r.LogoVersion
	}
	return r.LogoURL
}

// LogoContentType sniffs an uploaded logo: PNG, JPEG, GIF, WebP, ICO or
// SVG. It returns "" for anything else.
func LogoContentType(data []byte) string {
	if contentType := http.DetectContentType(data); logoTypes[contentType] {
		return contentType
	}
	// SVG is text; accept it when the document element is <svg>.
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Local == "svg" {
				return "image/svg+xml"
			}
			return ""
		}
	}
}

// SetLogoImage stores an uploaded logo, replacing an external URL.
func (m *Manager) SetLogoImage(ctx context.Context, data []byte, actorID string) error {
	if len(data) == 0 || len(data) > LogoMaxBytes {
		return fmt.Errorf("%w: Logo 图片不能为空，且不超过 %d KB", ErrInvalidSettings, LogoMaxBytes>>10)
	}
	contentType := LogoContentType(data)
	if contentType == "" {
		return fmt.Errorf("%w: 仅支持 SVG、PNG、JPG、GIF、WebP 或 ICO 图片", ErrInvalidSettings)
	}
	sum := sha256.Sum256(data)
	return m.updateLogo(ctx, `logo_url='',logo_image=$1,logo_content_type=$2,logo_version=$3`, "site_logo.uploaded", actorID, data, contentType, hex.EncodeToString(sum[:6]))
}

// SetLogoURL shows the logo from an external address, dropping an uploaded
// image; an empty URL restores the default mark.
func (m *Manager) SetLogoURL(ctx context.Context, raw, actorID string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return m.updateLogo(ctx, `logo_url='',logo_image=NULL,logo_content_type='',logo_version=''`, "site_logo.cleared", actorID)
	}
	if len(value) > 2000 || absoluteURL(value, true) != nil {
		return fmt.Errorf("%w: Logo 地址必须是完整的 http(s):// 图片地址", ErrInvalidSettings)
	}
	return m.updateLogo(ctx, `logo_url=$1,logo_image=NULL,logo_content_type='',logo_version=''`, "site_logo.linked", actorID, value)
}

// SetLogoMode sets how the logo sits beside the site name: auto, icon or
// wordmark (see SiteInput.LogoMode).
func (m *Manager) SetLogoMode(ctx context.Context, mode, actorID string) error {
	if mode != "auto" && mode != "icon" && mode != "wordmark" {
		return fmt.Errorf("%w: Logo 显示方式无效", ErrInvalidSettings)
	}
	return m.updateLogo(ctx, `logo_mode=$1`, "site_logo.mode_changed", actorID, mode)
}

func (m *Manager) updateLogo(ctx context.Context, assignments, action, actorID string, args ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE system_settings SET `+assignments+`,updated_at=now() WHERE singleton=true`, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("system settings are missing")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id) VALUES('staff',$1,$2,'system_settings','singleton')`, actorID, action); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return m.reload(ctx)
}

// LogoImage reads the uploaded logo from the database, so every API
// instance serves the current one.
func (m *Manager) LogoImage(ctx context.Context) (contentType string, data []byte, version string, err error) {
	err = m.db.QueryRow(ctx, `SELECT logo_content_type,coalesce(logo_image,''::bytea),logo_version FROM system_settings WHERE singleton=true`).Scan(&contentType, &data, &version)
	return contentType, data, version, err
}

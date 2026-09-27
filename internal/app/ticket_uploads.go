package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"vpsbill/internal/store/postgres"
)

// maxAttachmentsPerMessage bounds how many images one ticket message carries.
const maxAttachmentsPerMessage = 5

var attachmentTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// readTicketRequest decodes a ticket create or reply request into target.
// JSON bodies carry no attachments; multipart bodies put the same JSON in a
// "payload" field and images in "attachments" files. The content type of each
// image is sniffed from its bytes, never taken from the client.
func readTicketRequest(w http.ResponseWriter, r *http.Request, maxMB int, target any) ([]postgres.AttachmentUpload, bool) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		return nil, decodeJSON(w, r, target)
	}
	if maxMB < 1 {
		maxMB = 5
	}
	perFile := int64(maxMB) << 20
	r.Body = http.MaxBytesReader(w, r.Body, perFile*maxAttachmentsPerMessage+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "attachment_too_large", "message": fmt.Sprintf("附件总大小超出限制（每张不超过 %d MB）", maxMB)})
			return nil, false
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "请求内容无效"})
		return nil, false
	}
	defer r.MultipartForm.RemoveAll()
	decoder := json.NewDecoder(strings.NewReader(r.FormValue("payload")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "请求内容无效"})
		return nil, false
	}
	files := r.MultipartForm.File["attachments"]
	if len(files) > maxAttachmentsPerMessage {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "too_many_attachments", "message": fmt.Sprintf("每条消息最多 %d 张图片", maxAttachmentsPerMessage)})
		return nil, false
	}
	uploads := make([]postgres.AttachmentUpload, 0, len(files))
	for _, header := range files {
		name := attachmentName(header.Filename)
		if header.Size > perFile {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "attachment_too_large", "message": fmt.Sprintf("图片 %s 超过 %d MB", name, maxMB)})
			return nil, false
		}
		file, err := header.Open()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "无法读取上传的图片"})
			return nil, false
		}
		data, err := io.ReadAll(io.LimitReader(file, perFile+1))
		file.Close()
		if err != nil || len(data) == 0 || int64(len(data)) > perFile {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "attachment_invalid", "message": fmt.Sprintf("图片 %s 为空或超过 %d MB", name, maxMB)})
			return nil, false
		}
		contentType := http.DetectContentType(data)
		if !attachmentTypes[contentType] {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "attachment_type", "message": fmt.Sprintf("%s 不是支持的图片格式（PNG、JPEG、GIF、WebP）", name)})
			return nil, false
		}
		uploads = append(uploads, postgres.AttachmentUpload{FileName: name, ContentType: contentType, Data: data})
	}
	return uploads, true
}

// attachmentName keeps a readable base name and drops path parts and control
// characters.
func attachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		name = "image"
	}
	for utf8.RuneCountInString(name) > 120 {
		runes := []rune(name)
		name = string(runes[len(runes)-120:])
	}
	return name
}

// serveAttachment writes an image inline with headers that stop browsers
// from treating it as anything but an image.
func serveAttachment(w http.ResponseWriter, item postgres.TicketAttachment, data []byte) {
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": item.FileName}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

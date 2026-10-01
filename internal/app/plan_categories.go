package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"vpsbill/internal/store/postgres"
)

func (a *adminCatalog) listPlanCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := a.store.ListPlanCategories(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": categories})
}

func (a *adminCatalog) savePlanCategory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SortOrder   int    `json:"sort_order"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name, description := strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	switch {
	case name == "" || utf8.RuneCountInString(name) > 60:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "分类名称需为 1-60 个字"})
		return
	case utf8.RuneCountInString(description) > 500:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "分类描述最多 500 个字"})
		return
	case input.SortOrder < -10000 || input.SortOrder > 10000:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "排序值需在 -10000 到 10000 之间"})
		return
	}
	category, err := a.store.SavePlanCategory(r.Context(), r.PathValue("id"), postgres.PlanCategory{Name: name, Description: description, SortOrder: input.SortOrder})
	if writeCatalogNameError(w, err, "分类") {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"data": category})
}

func (a *adminCatalog) deletePlanCategory(w http.ResponseWriter, r *http.Request) {
	if writeCatalogNameError(w, a.store.DeletePlanCategory(r.Context(), r.PathValue("id")), "分类") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *adminCatalog) listPlanPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := a.store.ListPlanPresets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": presets})
}

func (a *adminCatalog) savePlanPreset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string          `json:"name"`
		Settings json.RawMessage `json:"settings"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	var settings map[string]any
	switch {
	case name == "" || utf8.RuneCountInString(name) > 60:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "模板名称需为 1-60 个字"})
		return
	case len(input.Settings) > 64<<10 || json.Unmarshal(input.Settings, &settings) != nil || settings == nil:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "模板内容无效"})
		return
	}
	preset, err := a.store.SavePlanPreset(r.Context(), r.PathValue("id"), postgres.PlanPreset{Name: name, Settings: input.Settings})
	if writeCatalogNameError(w, err, "模板") {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"data": preset})
}

func (a *adminCatalog) deletePlanPreset(w http.ResponseWriter, r *http.Request) {
	if writeCatalogNameError(w, a.store.DeletePlanPreset(r.Context(), r.PathValue("id")), "模板") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeCatalogNameError answers a failed category or preset write and
// reports whether it did.
func writeCatalogNameError(w http.ResponseWriter, err error, kind string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, postgres.ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "name_taken", "message": "已有同名" + kind})
	case errors.Is(err, postgres.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": kind + "不存在"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
	return true
}

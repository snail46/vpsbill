package app

import (
	"errors"
	"net/http"
	"strings"

	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

// Staff management: the super administrator adds staff and gives each one
// of the fixed roles (see migration 000047 for what each role may do).

func (a *authenticator) staffList(w http.ResponseWriter, r *http.Request) {
	members, err := a.store.StaffMembers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	roles, err := a.store.StaffRoles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"members": members, "roles": roles}})
}

func writeStaffError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrStaffNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "staff_not_found", "message": "管理员不存在"})
	case errors.Is(err, postgres.ErrRoleNotFound):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请选择有效的角色"})
	case errors.Is(err, postgres.ErrEmailExists):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "email_exists", "message": "该邮箱已经是管理员"})
	case errors.Is(err, postgres.ErrEmailIsCustomer):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "email_is_customer", "message": "该邮箱已注册为客户账号，请为管理员使用另一个邮箱"})
	case errors.Is(err, postgres.ErrStaffSelf):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "staff_self", "message": "不能修改自己的角色、停用或移除自己"})
	case errors.Is(err, postgres.ErrLastSuperAdmin):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "last_super_admin", "message": "至少要保留一名可用的超级管理员"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	}
}

func (a *authenticator) staffCreate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" || !validEmail(input.Email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "管理员姓名和有效邮箱不能为空"})
		return
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	member, err := a.store.CreateStaff(r.Context(), principalFromContext(r.Context()).UserID, input.Email, input.DisplayName, hash, input.Role)
	if err != nil {
		writeStaffError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": member})
}

func (a *authenticator) staffUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		writeStaffError(w, postgres.ErrStaffNotFound)
		return
	}
	var input struct {
		Role        *string `json:"role"`
		Status      *string `json:"status"`
		DisplayName *string `json:"display_name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Status != nil && *input.Status != "active" && *input.Status != "disabled" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "状态只能是启用或停用"})
		return
	}
	if input.DisplayName != nil && strings.TrimSpace(*input.DisplayName) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "管理员姓名不能为空"})
		return
	}
	member, err := a.store.UpdateStaff(r.Context(), principalFromContext(r.Context()).UserID, id, postgres.StaffChange{Role: input.Role, Status: input.Status, DisplayName: input.DisplayName})
	if err != nil {
		writeStaffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": member})
}

// staffResetPassword sets a staff member's password for them, for a
// forgotten password or a lost authenticator.
func (a *authenticator) staffResetPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		writeStaffError(w, postgres.ErrStaffNotFound)
		return
	}
	var input struct {
		Password string `json:"password"`
		ResetMFA bool   `json:"reset_mfa"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	actor := principalFromContext(r.Context()).UserID
	if actor == id {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "staff_self", "message": "修改自己的密码请到「安全中心」"})
		return
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	if err := a.store.ResetStaffPassword(r.Context(), actor, id, hash, input.ResetMFA); err != nil {
		writeStaffError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *authenticator) staffRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		writeStaffError(w, postgres.ErrStaffNotFound)
		return
	}
	if err := a.store.RemoveStaff(r.Context(), principalFromContext(r.Context()).UserID, id); err != nil {
		writeStaffError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

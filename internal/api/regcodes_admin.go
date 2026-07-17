package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *API) handleAdminListRegCodes(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListRegCodes()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取注册码失败")
		return
	}
	ok(w, list)
}

// POST /api/admin/reg-codes/generate {count, max_uses, note}
func (a *API) handleAdminGenerateRegCodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Count    int     `json:"count"`
		MaxUses  int64   `json:"max_uses"`
		Note     string  `json:"note"`
		GroupIDs []int64 `json:"group_ids"` // user groups granted on redemption
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	codes, err := a.st.GenerateRegCodes(req.Count, req.MaxUses, req.Note, req.GroupIDs)
	if err != nil {
		// Codes already returned are committed and correctly bound, so say how
		// many rather than implying nothing happened.
		if len(codes) > 0 {
			fail(w, http.StatusInternalServerError,
				fmt.Sprintf("仅成功生成 %d/%d 个后中断（已生成的可正常使用，请在下方列表查看）", len(codes), req.Count))
			return
		}
		fail(w, http.StatusInternalServerError, "生成失败")
		return
	}
	ok(w, J{"codes": codes})
}

// PUT /api/admin/reg-codes/{id} {enabled}
func (a *API) handleAdminUpdateRegCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.st.SetRegCodeEnabled(atoi(chi.URLParam(r, "id")), req.Enabled); err != nil {
		fail(w, http.StatusInternalServerError, "更新失败")
		return
	}
	ok(w, nil)
}

func (a *API) handleAdminDeleteRegCode(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteRegCode(atoi(chi.URLParam(r, "id"))); err != nil {
		fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	ok(w, nil)
}

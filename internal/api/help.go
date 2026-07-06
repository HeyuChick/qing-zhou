package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// GET /api/help — published help docs (any logged-in user).
func (a *API) handleHelpDocs(w http.ResponseWriter, r *http.Request) {
	docs, err := a.st.ListHelpDocs(true)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取失败")
		return
	}
	ok(w, docs)
}

// GET /api/admin/help — all help docs.
func (a *API) handleAdminHelpDocs(w http.ResponseWriter, r *http.Request) {
	docs, err := a.st.ListHelpDocs(false)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取失败")
		return
	}
	ok(w, docs)
}

type helpDocReq struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	SortOrder int    `json:"sort_order"`
	Published bool   `json:"published"`
}

func (a *API) handleAdminCreateHelpDoc(w http.ResponseWriter, r *http.Request) {
	var req helpDocReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		fail(w, http.StatusBadRequest, "请填写标题")
		return
	}
	id, err := a.st.CreateHelpDoc(req.Title, req.Content, req.SortOrder, req.Published)
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	ok(w, J{"id": id})
}

func (a *API) handleAdminUpdateHelpDoc(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	var req helpDocReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		fail(w, http.StatusBadRequest, "请填写标题")
		return
	}
	if err := a.st.UpdateHelpDoc(id, req.Title, req.Content, req.SortOrder, req.Published); err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	ok(w, J{"ok": true})
}

func (a *API) handleAdminDeleteHelpDoc(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteHelpDoc(atoi(chi.URLParam(r, "id"))); err != nil {
		fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	ok(w, J{"ok": true})
}

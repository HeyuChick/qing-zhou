package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// GET /api/user/announcements — active announcements (within window) with
// per-user read flags.
func (a *API) handleUserAnnouncements(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	list, err := a.st.ListActiveForUser(uid)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取公告失败")
		return
	}
	ok(w, list)
}

// POST /api/user/announcements/read — mark all currently-active announcements read.
func (a *API) handleUserMarkAnnouncementsRead(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(ctxUserID).(int64)
	list, err := a.st.ListActiveForUser(uid)
	if err != nil {
		fail(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	ids := make([]int64, 0, len(list))
	for _, an := range list {
		if !an.Read {
			ids = append(ids, an.ID)
		}
	}
	if err := a.st.MarkRead(uid, ids); err != nil {
		fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	ok(w, J{"marked": len(ids)})
}

// GET /api/admin/announcements — all announcements.
func (a *API) handleAdminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := a.st.ListAnnouncements(false)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取公告失败")
		return
	}
	ok(w, list)
}

func (a *API) handleAdminCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var an store.Announcement
	if err := json.NewDecoder(r.Body).Decode(&an); err != nil || an.Title == "" {
		fail(w, http.StatusBadRequest, "标题不能为空")
		return
	}
	id, err := a.st.CreateAnnouncement(an)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建公告失败")
		return
	}
	created, _ := a.st.GetAnnouncement(id)
	ok(w, created)
}

func (a *API) handleAdminUpdateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var an store.Announcement
	if err := json.NewDecoder(r.Body).Decode(&an); err != nil || an.Title == "" {
		fail(w, http.StatusBadRequest, "标题不能为空")
		return
	}
	an.ID = atoi(chi.URLParam(r, "id"))
	if err := a.st.UpdateAnnouncement(an); err != nil {
		fail(w, http.StatusInternalServerError, "更新公告失败")
		return
	}
	ok(w, nil)
}

func (a *API) handleAdminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	if err := a.st.DeleteAnnouncement(atoi(chi.URLParam(r, "id"))); err != nil {
		fail(w, http.StatusInternalServerError, "删除公告失败")
		return
	}
	ok(w, nil)
}

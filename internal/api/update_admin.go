package api

import (
	"context"
	"net/http"
	"time"

	"qingzhou/internal/version"
)

// handleUpdateCheck queries GitHub for the latest release and reports whether a
// newer version is available, along with the changelog (release body).
func (a *API) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := a.updater.Check(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w, res)
}

// handleUpdateStatus returns the current progress of an in-flight update, plus
// the running binary's version so the UI can detect a successful restart (the
// version flips once the re-execed process answers again).
func (a *API) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	st := a.updater.State()
	ok(w, J{
		"status":         st.Status,
		"message":        st.Message,
		"percent":        st.Percent,
		"target_version": st.TargetVersion,
		"started_at":     st.StartedAt,
		"current":        version.Current(),
	})
}

// handleUpdateApply starts the download → verify → swap → restart sequence in
// the background and returns immediately; the client polls the status endpoint.
func (a *API) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	if err := a.updater.Apply(time.Now().Unix()); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	ok(w, J{"started": true})
}

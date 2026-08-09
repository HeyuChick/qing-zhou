package api

import (
	"context"
	"encoding/json"
	"io"
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

// handleUpdateReleases lists the recent releases so the admin can pick a
// specific version instead of only ever taking "latest" — which is the only way
// off a release that turned out to be broken once it is no longer the newest.
func (a *API) handleUpdateReleases(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	list, err := a.updater.ListReleases(ctx, 30)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w, J{"current": version.Current(), "releases": list})
}

// handleUpdateApply starts the download → verify → swap → restart sequence in
// the background and returns immediately; the client polls the status endpoint.
//
// An optional {"version": "vX.Y.Z"} pins the release; omitting it keeps the
// original "install the latest" behaviour, so an old client calling this with
// no body is unaffected.
func (a *API) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	// A missing or empty body is the "latest" case, not an error.
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req)
	}
	if err := a.updater.ApplyVersion(time.Now().Unix(), req.Version); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	ok(w, J{"started": true, "version": req.Version})
}

// handleUpdateRollbackState reports whether the previous binary is still on
// disk, and which version it is.
func (a *API) handleUpdateRollbackState(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	ok(w, a.updater.RollbackState())
}

// handleUpdateRollback swaps the previously-installed binary back in. No
// network, no download — this is the path that still works when the running
// release is broken or GitHub is unreachable.
func (a *API) handleUpdateRollback(w http.ResponseWriter, r *http.Request) {
	if a.updater == nil {
		fail(w, http.StatusServiceUnavailable, "更新服务未启用")
		return
	}
	if err := a.updater.Rollback(time.Now().Unix()); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	ok(w, J{"started": true})
}

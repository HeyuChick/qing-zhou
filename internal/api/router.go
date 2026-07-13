package api

import (
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"qingzhou/internal/mailer"
	"qingzhou/internal/sbctl"
	"qingzhou/internal/store"
	"qingzhou/internal/updater"
	"qingzhou/web"
)

type API struct {
	st       *store.Store
	secret   []byte
	mailer   *mailer.Mailer // may be nil if SMTP is not configured
	authRL   *rateLimiter
	resendRL *rateLimiter
	probeRL  *rateLimiter // rate limit for agent report endpoint

	sbctl   *sbctl.Controller // native sing-box orchestrator; nil if not enabled
	updater *updater.Manager  // GitHub-release self-updater

	linkMu    sync.Mutex
	linkCache map[int64]linkCacheEntry
}

// SetSbController attaches the native sing-box controller so admin changes to
// inbounds/TLS/users can trigger a config rebuild. Safe to leave unset.
func (a *API) SetSbController(c *sbctl.Controller) { a.sbctl = c }

// sbRebuild regenerates and applies the sing-box config if the native
// controller is attached; a no-op otherwise. Errors are returned to the caller.
func (a *API) sbRebuild() error {
	if a.sbctl == nil {
		return nil
	}
	return a.sbctl.Rebuild()
}

func New(st *store.Store, secret []byte, mail *mailer.Mailer) *API {
	a := &API{
		st: st, secret: secret, mailer: mail,
		authRL:    newRateLimiter(20, time.Minute),   // 20 auth attempts / IP / min
		resendRL:  newRateLimiter(3, 10*time.Minute), // 3 verify resends / user / 10min
		probeRL:   newRateLimiter(60, time.Minute),   // 60 probe reports / IP / min
		linkCache: make(map[int64]linkCacheEntry),
	}
	// Self-updater: repo + optional GitHub token come from env or DB settings,
	// falling back to the project's canonical repo.
	a.updater = updater.New(
		func() string {
			if v := os.Getenv("QZ_UPDATE_REPO"); v != "" {
				return v
			}
			v, _ := st.GetSetting("update_repo")
			return v
		},
		func() string {
			if v := os.Getenv("QZ_UPDATE_GITHUB_TOKEN"); v != "" {
				return v
			}
			v, _ := st.GetSetting("update_github_token")
			return v
		},
	)
	return a
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// NOTE: middleware.RealIP is intentionally NOT used — it trusts client
	// X-Forwarded-For/X-Real-IP unconditionally, which would let any client spoof
	// its source IP and bypass the per-IP rate limiters. clientIP() honors those
	// headers only from a trusted proxy peer (see trustedproxy.go).
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	// Cap request bodies so an authenticated client can't drive the process into
	// memory pressure with a multi-GB POST. 8 MiB comfortably covers the largest
	// legitimate payload (pasted airport lists / sing-box config templates).
	r.Use(maxBodyMiddleware(8 << 20))

	// Public
	r.Get("/api/health", a.handleHealth)
	r.Get("/api/config", a.handleConfig)
	r.Get("/api/auth/verify", a.handleVerify)
	r.Get("/sub/{token}", a.handleSub)

	// Auth POST endpoints — rate limited per IP (brute-force / email-bomb).
	r.Group(func(pub chi.Router) {
		pub.Use(a.limit(a.authRL))
		pub.Post("/api/auth/login", a.handleLogin)
		pub.Post("/api/auth/register", a.handleRegister)
		pub.Post("/api/auth/forgot", a.handleForgot)
		pub.Post("/api/auth/reset", a.handleReset)
	})

	// Probe agent endpoints — rate limited, token-authenticated (no JWT).
	r.Group(func(pub chi.Router) {
		pub.Use(a.limit(a.probeRL))
		pub.Post("/api/monitor/report", a.handleMonitorReport)
	})
	r.Get("/api/monitor/agent/{arch}", a.handleDownloadAgent)
	r.Get("/api/monitor/install.sh", a.handleDownloadInstallScript)

	// Public monitoring dashboard (no auth required).
	r.Get("/api/monitor/public", a.handleMonitorPublic)
	r.Get("/api/monitor/public/sparklines", a.handleMonitorPublicSparklines)
	r.Get("/api/monitor/heatmap", a.handleMonitorPublicHeatmap)

	// Authenticated (any logged-in user)
	r.Group(func(pr chi.Router) {
		pr.Use(a.authMiddleware)
		pr.Get("/api/auth/me", a.handleMe)
		pr.Post("/api/auth/logout", a.handleLogout)
		pr.Get("/api/user/dashboard", a.handleDashboard)
		pr.Get("/api/user/plans", a.handleUserPlans)
		pr.Get("/api/user/subscription", a.handleSubscription)
		pr.Get("/api/user/proxies", a.handleUserProxies)
		pr.Put("/api/user/proxies/{bucket}", a.handleUpdateUserProxy)
		pr.Post("/api/user/reset-sub", a.handleResetSub)
		pr.Get("/api/user/packages", a.handleUserPackages)
		pr.Post("/api/user/purchase", a.handlePurchase)
		pr.Get("/api/user/orders", a.handleUserOrders)
		pr.Get("/api/user/points", a.handleUserPoints)
		pr.Get("/api/user/stats/traffic", a.handleUserTrafficStats)
		pr.Post("/api/user/password", a.handleChangePassword)
		pr.Post("/api/user/resend-verify", a.handleResendVerify)
		pr.Post("/api/user/email", a.handleBindEmail)
		pr.Get("/api/user/announcements", a.handleUserAnnouncements)
		pr.Post("/api/user/announcements/read", a.handleUserMarkAnnouncementsRead)
		pr.Get("/api/help", a.handleHelpDocs)
		pr.Get("/api/user/sessions", a.handleUserSessions)
		pr.Post("/api/user/sessions/{id}/revoke", a.handleUserRevokeSession)
		pr.Get("/api/user/nodes", a.handleUserNodes)
		pr.Get("/api/user/nodes/ping", a.handleUserNodesPing)
		pr.Post("/api/user/nodes/toggle", a.handleUserToggleNode)
		pr.Post("/api/user/nodes/bulk", a.handleUserBulkNodes)
		pr.Post("/api/user/nodes/disable-all", a.handleUserDisableAllNodes)
		pr.Post("/api/user/nodes/enable-all", a.handleUserEnableAllNodes)
	})

	// Admin only
	r.Group(func(ar chi.Router) {
		ar.Use(a.authMiddleware)
		ar.Use(a.requireAdmin)
		ar.Get("/api/admin/settings", a.handleGetSettings)
		ar.Put("/api/admin/settings", a.handlePutSettings)
		ar.Post("/api/admin/settings/test-smtp", a.handleTestSMTP)
		ar.Post("/api/admin/rebuild", a.handleAdminRebuild)
		ar.Get("/api/admin/update/check", a.handleUpdateCheck)
		ar.Get("/api/admin/update/status", a.handleUpdateStatus)
		ar.Post("/api/admin/update/apply", a.handleUpdateApply)
		ar.Get("/api/admin/help", a.handleAdminHelpDocs)
		ar.Post("/api/admin/help", a.handleAdminCreateHelpDoc)
		ar.Put("/api/admin/help/{id}", a.handleAdminUpdateHelpDoc)
		ar.Delete("/api/admin/help/{id}", a.handleAdminDeleteHelpDoc)
		ar.Delete("/api/admin/users/{id}", a.handleAdminDeleteUser)
		ar.Post("/api/admin/users/{id}/points", a.handleAdminRecharge)
		ar.Post("/api/admin/users/{id}/assign-plan", a.handleAdminAssignPlan)
		ar.Get("/api/admin/packages", a.handleAdminListPackages)
		ar.Post("/api/admin/packages", a.handleAdminCreatePackage)
		ar.Put("/api/admin/packages/{id}", a.handleAdminUpdatePackage)
		ar.Post("/api/admin/packages/{id}/retire", a.handleAdminRetirePackage)
		ar.Post("/api/admin/packages/{id}/enable", a.handleAdminEnablePackage)
		ar.Delete("/api/admin/packages/{id}", a.handleAdminDeletePackage)
		ar.Get("/api/admin/orders", a.handleAdminListOrders)
		ar.Get("/api/admin/users/{id}/orders", a.handleAdminUserOrders)
		ar.Get("/api/admin/users/{id}/plans", a.handleAdminUserPlans)
		ar.Get("/api/admin/orders/{id}/refund-preview", a.handleAdminRefundPreview)
		ar.Post("/api/admin/orders/{id}/refund", a.handleAdminRefundOrder)
		ar.Delete("/api/admin/orders/{id}", a.handleAdminDeleteOrder)

		// native sing-box (B2): TLS/Reality profiles, inbounds, config preview
		ar.Post("/api/admin/sb/reality-keypair", a.handleAdminRealityKeypair)
		ar.Get("/api/admin/sb/sni-test", a.handleAdminSniTest)
		ar.Get("/api/admin/sb/tls", a.handleAdminListSbTls)
		ar.Post("/api/admin/sb/tls", a.handleAdminSaveSbTls)
		ar.Post("/api/admin/sb/tls/reality", a.handleAdminCreateRealityTls)
		ar.Put("/api/admin/sb/tls/reality/{id}", a.handleAdminUpdateRealityTls)
		ar.Post("/api/admin/sb/tls/self-signed", a.handleAdminSelfSignedCert)
		ar.Post("/api/admin/sb/tls/quick-selfsigned", a.handleAdminQuickSelfSignedTls)
		ar.Post("/api/admin/sb/tls/acme", a.handleAdminAcmeCert)
		ar.Post("/api/admin/sb/tls/cert", a.handleAdminSaveCertTls)
		ar.Put("/api/admin/sb/tls/cert/{id}", a.handleAdminSaveCertTls)
		ar.Put("/api/admin/sb/tls/{id}", a.handleAdminSaveSbTls)
		ar.Delete("/api/admin/sb/tls/{id}", a.handleAdminDeleteSbTls)
		ar.Get("/api/admin/sb/inbounds", a.handleAdminListSbInbounds)
		ar.Post("/api/admin/sb/inbounds", a.handleAdminSaveSbInbound)
		ar.Put("/api/admin/sb/inbounds/{id}", a.handleAdminSaveSbInbound)
		ar.Delete("/api/admin/sb/inbounds/{id}", a.handleAdminDeleteSbInbound)
		ar.Get("/api/admin/sb/preview", a.handleAdminSbPreview)
		ar.Get("/api/admin/sb/check", a.handleAdminSbCheck)
		ar.Get("/api/admin/sb/port-check", a.handleAdminPortCheck)
		ar.Get("/api/admin/sb/import-remote/list-files", a.handleAdminImportRemoteListFiles)
		ar.Get("/api/admin/sb/import-remote/preview", a.handleAdminImportRemotePreview)

		// server management (multi-server sing-box orchestration)
		ar.Get("/api/admin/servers", a.handleAdminListServers)
		ar.Post("/api/admin/servers", a.handleAdminCreateServer)
		ar.Put("/api/admin/servers/{id}", a.handleAdminUpdateServer)
		ar.Delete("/api/admin/servers/{id}", a.handleAdminDeleteServer)
		ar.Post("/api/admin/servers/{id}/test", a.handleAdminTestServer)
		ar.Post("/api/admin/servers/{id}/rebuild", a.handleAdminRebuildServer)
		ar.Put("/api/admin/servers/{id}/monitor", a.handleUpdateServerMonitor)

		// monitor probe
		ar.Get("/api/admin/monitor/dashboard", a.handleMonitorDashboard)
		ar.Get("/api/admin/monitor/servers", a.handleMonitorServers)
		ar.Get("/api/admin/monitor/servers/{id}/metrics", a.handleServerMetrics)
		ar.Get("/api/admin/monitor/heatmap", a.handleMonitorHeatmap)
		ar.Get("/api/admin/monitor/alerts", a.handleMonitorAlerts)
		ar.Post("/api/admin/monitor/alerts/{id}/read", a.handleMarkAlertRead)

		// nodes / groups / sources (Phase 4.5)
		ar.Get("/api/admin/inbounds", a.handleAdminInbounds)
		ar.Get("/api/admin/nodes", a.handleAdminListNodes)
		ar.Post("/api/admin/nodes", a.handleAdminCreateNode)
		ar.Post("/api/admin/nodes/import", a.handleAdminImportNodes)
		ar.Put("/api/admin/nodes/{id}", a.handleAdminUpdateNode)
		ar.Delete("/api/admin/nodes/{id}", a.handleAdminDeleteNode)
		ar.Get("/api/admin/node-groups", a.handleAdminListGroups)
		ar.Post("/api/admin/node-groups", a.handleAdminCreateGroup)
		ar.Put("/api/admin/node-groups/{id}", a.handleAdminUpdateGroup)
		ar.Delete("/api/admin/node-groups/{id}", a.handleAdminDeleteGroup)
		ar.Get("/api/admin/node-sources", a.handleAdminListSources)
		ar.Post("/api/admin/node-sources", a.handleAdminCreateSource)
		ar.Put("/api/admin/node-sources/{id}", a.handleAdminUpdateSource)
		ar.Post("/api/admin/node-sources/{id}/fetch", a.handleAdminFetchSource)
		ar.Delete("/api/admin/node-sources/{id}", a.handleAdminDeleteSource)

		// users + stats (Phase 5)
		ar.Get("/api/admin/users", a.handleAdminListUsers)
		ar.Post("/api/admin/users", a.handleAdminCreateUser)
		ar.Put("/api/admin/users/{id}", a.handleAdminUpdateUser)
		ar.Get("/api/admin/stats/overview", a.handleAdminOverview)
		ar.Get("/api/admin/stats/traffic", a.handleAdminTrafficStats)
		ar.Get("/api/admin/stats/top", a.handleAdminTopStats)
		ar.Get("/api/admin/stats/distribution", a.handleAdminDistribution)

		ar.Get("/api/admin/reg-codes", a.handleAdminListRegCodes)
		ar.Post("/api/admin/reg-codes/generate", a.handleAdminGenerateRegCodes)
		ar.Put("/api/admin/reg-codes/{id}", a.handleAdminUpdateRegCode)
		ar.Delete("/api/admin/reg-codes/{id}", a.handleAdminDeleteRegCode)

		ar.Get("/api/admin/announcements", a.handleAdminListAnnouncements)
		ar.Post("/api/admin/announcements", a.handleAdminCreateAnnouncement)
		ar.Put("/api/admin/announcements/{id}", a.handleAdminUpdateAnnouncement)
		ar.Delete("/api/admin/announcements/{id}", a.handleAdminDeleteAnnouncement)
	})

	// sing-box one-click install script. Served explicitly (above the SPA
	// catch-all) from the embedded web/dist so `curl https://<panel>/install-singbox.sh | bash`
	// works under any frontend — the new frontend/dist doesn't bundle it, so
	// without this the request would fall through to index.html.
	r.Get("/install-singbox.sh", serveInstallScript)

	// Embedded SPA (must be last; specific routes above take precedence).
	// Set QZ_USE_NEW_FRONTEND=1 to serve the new Vue 3 frontend from the
	// frontend/ package. Otherwise, the original web/ frontend is used.
	r.Handle("/*", frontendHandler())

	return r
}

// serveInstallScript serves the embedded sing-box install script as a shell
// script, independent of which SPA frontend is active.
func serveInstallScript(w http.ResponseWriter, r *http.Request) {
	b, ok := web.InstallScript()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	_, _ = w.Write(b)
}

// frontendHandler returns the appropriate SPA handler based on environment.
// QZ_USE_NEW_FRONTEND=1 selects the new Vue 3 frontend (frontend package).
// QZ_WEB_DIR can point to either frontend/dist (new) or web/dist (old) for dev.
func frontendHandler() http.Handler {
	if os.Getenv("QZ_USE_NEW_FRONTEND") == "1" {
		return newFrontendHandler()
	}
	return web.Handler()
}

package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"qingzhou/internal/acmesh"
	"qingzhou/internal/store"
)

// renewCutoffDays renews an ACME certificate once it has this many days or fewer
// left. Let's Encrypt issues 90-day certs and acme.sh renews at ~30 days
// remaining, so this threshold aligns with acme.sh's own behavior.
const renewCutoffDays = 30

// StartCertRenew runs a periodic sweep that renews auto-renew ACME certificates
// nearing expiry, writes the fresh PEM back to the DB, and re-pushes config to
// exactly the servers that carry each cert. The panel host owns issuance (DNS-01
// needs no node access), so remote nodes get auto-renew without any per-node
// cron. Mirrors StartMonitorTasks' ticker pattern.
func (a *API) StartCertRenew(ctx context.Context, interval time.Duration) {
	go func() {
		// Catch-up shortly after boot, but give the sing-box controller time to
		// attach and finish its first rebuild before we might re-push renewed certs.
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}
		a.renewDueCerts(ctx)

		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.renewDueCerts(ctx)
			}
		}
	}()
}

// renewDueCerts renews every auto-renew ACME cert within renewCutoffDays of
// expiry. Failures are isolated per-cert (logged + recorded in last_error) so
// one bad domain never blocks the others.
func (a *API) renewDueCerts(ctx context.Context) {
	certs, err := a.st.ListCerts()
	if err != nil {
		log.Printf("cert renew: list: %v", err)
		return
	}
	now := time.Now().Unix()
	cfToken, _ := a.st.GetSetting("cf_api_token")
	certDir := a.certDir()
	for _, c := range certs {
		if !c.AutoRenew || c.Source != "acme" || c.DecryptFailed {
			continue
		}
		if c.NotAfter == 0 || c.NotAfter-now > int64(renewCutoffDays)*86400 {
			continue // not due yet
		}
		if err := a.renewOneCert(ctx, c, cfToken, certDir, false); err != nil {
			log.Printf("cert renew: %s (%s): %v", c.Name, c.Domain, err)
			_ = a.st.SetCertRenewError(c.ID, err.Error())
		}
	}
}

// renewOneCert renews a single cert via acme.sh, reads the refreshed PEM back
// into the DB, and re-pushes to the servers that use it. force=true skips
// acme.sh's own "still valid" threshold (manual "renew now").
func (a *API) renewOneCert(ctx context.Context, c *store.Cert, cfToken, certDir string, force bool) error {
	method := acmesh.Method(c.AcmeMethod)
	if method == "" {
		method = acmesh.MethodCFDNS
	}
	if method == acmesh.MethodCFDNS && strings.TrimSpace(cfToken) == "" {
		return fmt.Errorf("缺少 Cloudflare API Token（请在系统设置填写）")
	}
	rctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	res, err := acmesh.Renew(rctx, acmesh.LocalRunner{}, acmesh.IssueOpts{
		Domain:  c.Domain,
		Method:  method,
		CFToken: strings.TrimSpace(cfToken),
		CertDir: certDir,
	}, force)
	if err != nil {
		return err
	}
	certPEM, keyPEM, err := acmesh.ReadPEM(rctx, acmesh.LocalRunner{}, res)
	if err != nil {
		return err
	}
	c.CertPEM, c.KeyPEM = certPEM, keyPEM
	c.LastRenewAt = time.Now().Unix()
	c.LastError = ""
	if _, err := a.st.SaveCert(c); err != nil {
		return err
	}
	a.pushCertServers(c.ID)
	return nil
}

// pushCertServers re-pushes config to the servers whose inbounds reference the
// given cert. An unbound cert (no referencing inbound) needs no push.
func (a *API) pushCertServers(certID int64) {
	ids, err := a.st.CertServerIDs(certID)
	if err != nil {
		a.sbRebuildLog() // fall back to a full rebuild on lookup error
		return
	}
	if len(ids) == 0 {
		return
	}
	a.sbScheduleServer(ids...)
}

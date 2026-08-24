package sbctl

import (
	"context"
	"time"

	"qingzhou/internal/sbver"
	"qingzhou/internal/store"
)

// localVersionTTL is how long a local version probe is reused. The binary only
// changes when someone installs one, so re-running the command on every
// Rebuild tick would be pure noise.
const localVersionTTL = 10 * time.Minute

// VersionReporter is the part of the local process manager that can report
// which sing-box is installed (satisfied by *sbproc.Manager). Optional: a
// controller built with a bare Applier simply has no local version to show.
type VersionReporter interface {
	Version(ctx context.Context) (string, error)
}

// refreshLocalVersion records the panel machine's own sing-box, at most once
// per localVersionTTL.
//
// The remote nodes are covered by the stats capability probe, which already
// runs the same command over SSH. The local one has no such path, and it is the
// node an operator is most likely to have installed by hand — so without this
// it would be the one machine the panel can say nothing about.
func (c *Controller) refreshLocalVersion(force bool) {
	rep, ok := c.mgr.(VersionReporter)
	if !ok {
		return
	}
	c.localVerMu.Lock()
	if !force && !c.localVerAt.IsZero() && time.Since(c.localVerAt) < localVersionTTL {
		c.localVerMu.Unlock()
		return
	}
	c.localVerAt = time.Now()
	c.localVerMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := rep.Version(ctx)
	if err != nil {
		// Not necessarily a fault: a panel that only manages remote nodes has no
		// local sing-box at all. Recorded rather than logged so the UI can say so
		// plainly instead of leaving a blank row.
		_ = c.st.SetNodeSingboxError(store.LocalNodeID, err.Error())
		return
	}
	_ = c.st.SetNodeSingbox(store.LocalNodeID, sbver.Parse(out))
}

// RefreshVersions re-probes every node's sing-box now, ignoring the caches that
// normally keep the probes cheap. Backs the panel's "重新检测" button: after an
// upgrade the operator wants the new number immediately, not in ten minutes.
func (c *Controller) RefreshVersions() {
	c.refreshLocalVersion(true)
	servers, err := c.st.ListServers()
	if err != nil {
		return
	}
	for _, sv := range servers {
		if !sv.Enabled {
			continue
		}
		// A forced refresh must drop both caches. The binary may have just been
		// reinstalled at a different path, while remoteMgr survives for the whole
		// panel process.
		c.invalidateRemoteCaches(sv.ID)
		c.statsSupported(sv)
	}
}

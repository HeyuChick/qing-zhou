package api

import (
	"context"
	"log"
	"time"
)

// StartQueueAdvance periodically promotes queued plan buckets whose active head
// has been exhausted or has expired, so the next same-package purchase takes
// over. Purchases advance their own user synchronously (in the buy transaction);
// this ticker covers the later exhaustion/expiry transitions, which have no
// synchronous trigger. A promotion changes which sing-box identity is live, so a
// config rebuild is scheduled whenever anything advanced. Mirrors the
// StartCertRenew ticker pattern.
func (a *API) StartQueueAdvance(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				changed, err := a.st.AdvanceAllQueues()
				if err != nil {
					log.Printf("queue advance: %v", err)
					continue
				}
				if len(changed) > 0 {
					log.Printf("queue advance: promoted next plan for %d user(s)", len(changed))
					a.sbRebuildLog()
				}
			}
		}
	}()
}

package sbctl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"qingzhou/internal/store"
)

var legacyNumericClient = regexp.MustCompile(`^relay_[1-9][0-9]*$`)

func configNeedsRelayNamespaceProof(raw []byte) bool {
	var cfg struct {
		Inbounds []struct {
			Type  string           `json:"type"`
			Users []map[string]any `json:"users"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return false
	}
	for _, ib := range cfg.Inbounds {
		if ib.Type != "mixed" {
			continue
		}
		for _, u := range ib.Users {
			if name, ok := u["username"].(string); ok && legacyNumericClient.MatchString(name) {
				return true
			}
		}
	}
	return false
}

// The config bytes were successfully applied by the caller. Verify the installed
// hash and live systemd invocation together before asserting namespace separation.
// This is never called merely because a desired configuration was generated.
func (c *Controller) rememberRelayNamespace(serverID int64, raw []byte) {
	if !configNeedsRelayNamespaceProof(raw) {
		return
	}
	writer, ok := c.st.(interface {
		RecordRelayNamespaceEpoch(int64, string, []byte) error
	})
	if !ok {
		return
	}
	var sv *store.Server
	var err error
	if serverID != 0 {
		sv, err = c.st.GetServer(serverID)
		if err != nil || sv == nil {
			c.recordTrafficFailure(serverID, "legacy_identity_unverified")
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	epoch, err := c.verifiedAppliedNamespaceEpoch(ctx, sv, raw)
	if err == nil {
		err = writer.RecordRelayNamespaceEpoch(serverID, epoch, raw)
	}
	if err != nil {
		c.recordTrafficFailure(serverID, "legacy_identity_unverified")
		log.Printf("sbctl: server %d legacy customer statistics require a verified applied process epoch; attribution remains unavailable", serverID)
	}
}

func (c *Controller) verifiedAppliedNamespaceEpoch(ctx context.Context, sv *store.Server, raw []byte) (string, error) {
	sum := sha256.Sum256(raw)
	return c.verifyNamespaceConfigHash(ctx, sv, hex.EncodeToString(sum[:]))
}

func (c *Controller) verifyNamespaceConfigHash(ctx context.Context, sv *store.Server, hash string) (string, error) {
	unit, path := "sing-box", c.localConfigPath()
	if sv != nil {
		path = serverConfigPath(sv)
		if sv.SystemdUnit != "" {
			unit = sv.SystemdUnit
		}
	}
	script := "set -eu; boot=$(cat /proc/sys/kernel/random/boot_id); inv1=$(systemctl show --property=InvocationID --value -- " + quoteEpochUnit(unit) + "); systemctl is-active --quiet -- " + quoteEpochUnit(unit) + "; digest=$(sha256sum -- " + quoteEpochUnit(path) + "); digest=${digest%% *}; inv2=$(systemctl show --property=InvocationID --value -- " + quoteEpochUnit(unit) + "); test \"$inv1\" = \"$inv2\"; test \"$digest\" = " + quoteEpochUnit(hash) + "; printf '%s:%s' \"$boot\" \"$inv1\""
	var out string
	var err error
	if sv != nil && !isLocalHostContext(ctx, sv.Host) {
		if c.remoteMgr == nil {
			return "", fmt.Errorf("remote manager unavailable")
		}
		out, err = c.remoteMgr.RunCommand(ctx, SSHConfigFor(sv), script)
	} else {
		command := exec.CommandContext(ctx, "sh", "-c", script)
		command.WaitDelay = 2 * time.Second
		var b []byte
		b, err = command.Output()
		out = string(b)
	}
	out = strings.TrimSpace(out)
	if err != nil || !processEpochRE.MatchString(out) {
		return "", fmt.Errorf("cannot verify applied config and active process epoch")
	}
	return out, nil
}

func (c *Controller) confirmCurrentRelayNamespace(ctx context.Context, serverID int64, sv *store.Server, epoch string) error {
	proofs, ok := c.st.(interface {
		HasLegacyNumericCustomers() (bool, error)
		RelayNamespaceEpochKnown(int64, string) (bool, error)
		LatestRelayNamespaceProof(int64) (string, error)
		ConfirmRelayNamespaceEpoch(int64, string, string) error
	})
	if !ok || epoch == "" {
		return nil
	}
	required, err := proofs.HasLegacyNumericCustomers()
	if err != nil || !required {
		return err
	}
	known, err := proofs.RelayNamespaceEpochKnown(serverID, epoch)
	if err != nil || known {
		return err
	}
	hash, err := proofs.LatestRelayNamespaceProof(serverID)
	if err != nil || hash == "" {
		return err
	}
	verified, err := c.verifyNamespaceConfigHash(ctx, sv, hash)
	if err != nil || verified != epoch {
		return fmt.Errorf("legacy account attribution requires a verified applied process epoch")
	}
	return proofs.ConfirmRelayNamespaceEpoch(serverID, epoch, hash)
}

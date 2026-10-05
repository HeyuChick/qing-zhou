package api

import (
	"qingzhou/internal/subconv"
	"testing"
)

func TestNodeEntryTrustedLegacyKeyAndExternalClaims(t *testing.T) {
	link := "vless://u@example.test:443?type=ws&security=tls&path=%2Fws&alpn=http%2F1.1"
	old := "vless://u@example.test:443?type=ws&security=tls&path=%2Fws"
	disabled := map[string]bool{subconv.NodeKey(old): true}
	trusted := nodeEntry{Link: link, LegacyKeys: subconv.NodeKeys(old)}
	if !trusted.disabled(disabled) {
		t.Fatal("upgraded self-built node revived despite old disabled key")
	}
	if trusted.nodeKeys()[0] != subconv.NodeKey(link) {
		t.Fatal("current node key must stay first")
	}
	external := nodeEntry{Link: link + "&qz-node-key=" + subconv.NodeKey(old)}
	if external.disabled(disabled) {
		t.Fatal("untrusted URI supplied a compatibility key")
	}
}

package subconv

import "testing"

// TestNodeKeyIgnoresTuningParams is the regression guard for a silent blocklist
// wipe: self-built links are re-rendered from the sing-box config on every
// request, so if NodeKey covered the tuning params, adding one (packetEncoding,
// or an admin flipping mux/tfo on the inbound) would move every node's key and
// the user's "disable this node" rows would quietly stop matching — the hidden
// nodes just come back.
func TestNodeKeyIgnoresTuningParams(t *testing.T) {
	base := "vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com#n1"
	want := NodeKey(base)

	variants := []string{
		"vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com&packetEncoding=xudp#n1",
		"vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com&tfo=1&mptcp=1#n1",
		"vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com&mux=1&brutal_up=50&brutal_down=200#n1",
		// param order must not matter either
		"vless://u@1.2.3.4:443?sni=a.com&sid=ab&pbk=PBK&security=reality#n1",
		// the #remark is the node's display name and changes on its own
		"vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com#renamed",
	}
	for _, v := range variants {
		if got := NodeKey(v); got != want {
			t.Errorf("key moved\n  base: %s -> %s\n  var:  %s -> %s", base, want, v, got)
		}
	}
}

// TestNodeKeyDistinguishesNodes guards the other direction: dropping params from
// the hash must not collapse genuinely different nodes onto one key, or
// disabling one would disable its neighbours.
func TestNodeKeyDistinguishesNodes(t *testing.T) {
	links := map[string]string{
		"base":      "vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com#n",
		"port":      "vless://u@1.2.3.4:8443?security=reality&pbk=PBK&sid=ab&sni=a.com#n",
		"host":      "vless://u@5.6.7.8:443?security=reality&pbk=PBK&sid=ab&sni=a.com#n",
		"uuid":      "vless://u2@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com#n",
		"sni":       "vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=b.com#n",
		"transport": "vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com&type=ws&path=/x#n",
		"wspath":    "vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com&type=ws&path=/y#n",
	}
	seen := map[string]string{}
	for name, link := range links {
		k := NodeKey(link)
		if prev, dup := seen[k]; dup {
			t.Errorf("%s and %s collapsed onto key %s", prev, name, k)
		}
		seen[k] = name
	}
}

// TestNodeKeysCoversLegacy pins what the un-block path has to delete. Clients
// only ever see NodeKeys(link)[0], so if this dropped the legacy key an old row
// would survive the delete and the node would stay hidden no matter how many
// times the user flipped the toggle.
func TestNodeKeysCoversLegacy(t *testing.T) {
	tuned := "vless://u@1.2.3.4:443?security=tls&sni=a.com&packetEncoding=xudp#n1"
	keys := NodeKeys(tuned)
	if len(keys) != 2 {
		t.Fatalf("a link carrying tuning params has two keys, got %v", keys)
	}
	if keys[0] != NodeKey(tuned) {
		t.Errorf("the current key must come first, got %v", keys)
	}
	if keys[1] != legacyNodeKey(tuned) {
		t.Errorf("the legacy key must be included, got %v", keys)
	}

	// A link with nothing volatile in it hashes the same both ways — no point
	// handing the delete path a duplicate.
	plain := "vless://u@1.2.3.4:443?security=tls&sni=a.com#n1"
	if keys := NodeKeys(plain); len(keys) != 1 {
		t.Errorf("a link with no tuning params has one key, got %v", keys)
	}
}

// TestNodeDisabledHonorsLegacyKey covers blocklist rows written before the
// tuning params were excluded from the hash. The link they were computed from
// is long gone, so the old key has to be recomputed from the live link.
func TestNodeDisabledHonorsLegacyKey(t *testing.T) {
	link := "vless://u@1.2.3.4:443?security=tls&sni=a.com&packetEncoding=xudp#n1"

	legacy := map[string]bool{legacyNodeKey(link): true}
	if !NodeDisabled(legacy, link) {
		t.Error("a row stored under the pre-canonicalisation key must still hide the node")
	}
	current := map[string]bool{NodeKey(link): true}
	if !NodeDisabled(current, link) {
		t.Error("a row stored under the current key must hide the node")
	}
	if NodeDisabled(map[string]bool{"deadbeefdeadbeef": true}, link) {
		t.Error("an unrelated key must not hide the node")
	}
	if NodeDisabled(nil, link) {
		t.Error("an empty blocklist must hide nothing")
	}
}

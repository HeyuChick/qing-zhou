package store

import (
	"encoding/json"
	"testing"
)

func withRawTransport(t *testing.T, raw []byte, transport string) []byte {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"inbounds", "outbounds"} {
		values, _ := cfg[field].([]any)
		for _, value := range values {
			endpoint := value.(map[string]any)
			if endpoint["type"] == "vless" || endpoint["type"] == "trojan" {
				endpoint["transport"] = map[string]any{"type": transport, "path": "/raw-proof"}
			}
		}
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRelayCoreConfigProofUsesRawAcrossOptionsABAAndOptOut(t *testing.T) {
	for _, transport := range []string{"ws", "httpupgrade"} {
		f := newUserMeteringFixture(t, 2)
		target := withRawTransport(t, f.apply(t, 1), transport)
		source := withRawTransport(t, f.apply(t, 0), transport)
		// Desired options remain plaintext (the A in A→B→A), and the latest
		// switches are off. Neither can erase requirements already in these bytes.
		if err := f.st.ConfigureTrafficMetering(false, false, false); err != nil {
			t.Fatal(err)
		}
		for i, raw := range [][]byte{source, target} {
			required, err := f.st.RelayCoreRequirementsForConfig(f.servers[i], raw)
			if err != nil || !required.TransportReadBuffer || required.VisionFraming {
				t.Fatalf("raw %s hop %d ignored: %+v %v", transport, i, required, err)
			}
		}
	}
}

func TestRelayCoreConfigProofPreservesPureP0Behavior(t *testing.T) {
	st, a, b, _, _, users := meteringRelayFixture(t)
	if err := st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	target, err := st.BuildSingboxConfigForServer(b, "{}", "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRelayConfigApplied(b, target); err != nil {
		t.Fatal(err)
	}
	source, err := st.BuildSingboxConfigForServer(a, "{}", "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	for id, raw := range map[int64][]byte{a: source, b: target} {
		required, err := st.RelayCoreRequirementsForConfig(id, withRawTransport(t, raw, "ws"))
		if err != nil || required != (RelayCoreRequirements{}) {
			t.Fatalf("P0 shared auth acquired P1 gate: %+v %v", required, err)
		}
	}
}

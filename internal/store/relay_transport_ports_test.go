package store

import "testing"

func TestRelayProtocolTransportPortCollision(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		t.Run(protocol, func(t *testing.T) {
			st := newRefundStore(t)
			_, err := st.SaveSbInbound(&SbInbound{Type: protocol, Tag: "quic-entry", Listen: "127.0.0.1", ListenPort: 2443, Options: `{"transport":{"type":"quic"}}`, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []struct {
				protocol, options string
				conflict          bool
			}{
				{"tuic", `{}`, true}, {"hysteria", `{}`, true}, {"hysteria2", `{}`, true},
				{"vless", `{}`, false}, {"vmess", `{"transport":{"type":"ws"}}`, false},
				{"trojan", `{"transport":{"type":"quic"}}`, true},
				{"shadowsocks", `{"network":["tcp"]}`, false},
				{"shadowsocks", `{"network":["udp"]}`, true},
				{"shadowsocks", `{"network":["tcp","udp"]}`, true},
			} {
				conflict, _, err := st.SbInboundPortConflict(&SbInbound{Type: candidate.protocol, Listen: "127.0.0.1", ListenPort: 2443, Options: candidate.options})
				if err != nil || conflict != candidate.conflict {
					t.Fatalf("%s %s: conflict=%v want=%v err=%v", candidate.protocol, candidate.options, conflict, candidate.conflict, err)
				}
			}
		})
	}
}

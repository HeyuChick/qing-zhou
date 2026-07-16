package store

import (
	"path/filepath"
	"testing"
)

func newInboundStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// Port sharing is only a conflict when the two inbounds actually want the same
// L4 protocol on an overlapping listen address. hysteria2 (QUIC/UDP) alongside
// vless (TCP) on 443 is a legitimate, common pairing and must be allowed.
func TestSbInboundPortConflict(t *testing.T) {
	cases := []struct {
		name     string
		existing SbInbound
		probe    SbInbound
		want     bool
	}{
		{
			name:     "UDP hysteria2 and TCP vless share a port",
			existing: SbInbound{Type: "hysteria2", Tag: "hy2", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "vless", Tag: "vless", Listen: "::", ListenPort: 443},
			want:     false,
		},
		{
			name:     "two TCP inbounds on one port collide",
			existing: SbInbound{Type: "vless", Tag: "vless", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "trojan", Tag: "trojan", Listen: "::", ListenPort: 443},
			want:     true,
		},
		{
			name:     "two QUIC inbounds on one port collide",
			existing: SbInbound{Type: "hysteria2", Tag: "hy2", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "tuic", Tag: "tuic", Listen: "::", ListenPort: 443},
			want:     true,
		},
		{
			name:     "shadowsocks binds both, so it collides with TCP",
			existing: SbInbound{Type: "shadowsocks", Tag: "ss", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "vless", Tag: "vless", Listen: "::", ListenPort: 443},
			want:     true,
		},
		{
			name:     "shadowsocks binds both, so it collides with UDP too",
			existing: SbInbound{Type: "shadowsocks", Tag: "ss", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "tuic", Tag: "tuic", Listen: "::", ListenPort: 443},
			want:     true,
		},
		{
			name:     "shadowsocks narrowed to udp frees the tcp side",
			existing: SbInbound{Type: "shadowsocks", Tag: "ss", Listen: "::", ListenPort: 443, Options: `{"network":"udp"}`},
			probe:    SbInbound{Type: "vless", Tag: "vless", Listen: "::", ListenPort: 443},
			want:     false,
		},
		{
			name:     "distinct listen addresses do not overlap",
			existing: SbInbound{Type: "vless", Tag: "a", Listen: "10.0.0.1", ListenPort: 443},
			probe:    SbInbound{Type: "trojan", Tag: "b", Listen: "10.0.0.2", ListenPort: 443},
			want:     false,
		},
		{
			name:     "wildcard listen overlaps a specific address",
			existing: SbInbound{Type: "vless", Tag: "a", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "trojan", Tag: "b", Listen: "10.0.0.2", ListenPort: 443},
			want:     true,
		},
		{
			name:     "different ports never conflict",
			existing: SbInbound{Type: "vless", Tag: "a", Listen: "::", ListenPort: 443},
			probe:    SbInbound{Type: "trojan", Tag: "b", Listen: "::", ListenPort: 8443},
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newInboundStore(t)
			ex := tc.existing
			if ex.Options == "" {
				ex.Options = "{}"
			}
			if _, err := st.SaveSbInbound(&ex); err != nil {
				t.Fatal(err)
			}
			probe := tc.probe
			if probe.Options == "" {
				probe.Options = "{}"
			}
			got, tag, err := st.SbInboundPortConflict(&probe)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("conflict = %v (tag %q), want %v", got, tag, tc.want)
			}
		})
	}
}

// Editing an inbound must not report the inbound colliding with itself.
func TestSbInboundPortConflict_ExcludesSelf(t *testing.T) {
	st := newInboundStore(t)
	ib := SbInbound{Type: "vless", Tag: "vless", Listen: "::", ListenPort: 443, Options: "{}"}
	id, err := st.SaveSbInbound(&ib)
	if err != nil {
		t.Fatal(err)
	}
	ib.ID = id
	if conflict, tag, err := st.SbInboundPortConflict(&ib); err != nil {
		t.Fatal(err)
	} else if conflict {
		t.Fatalf("editing an inbound must not conflict with itself (tag %q)", tag)
	}
}

// The same port on two different servers is not a conflict.
func TestSbInboundPortConflict_PerServer(t *testing.T) {
	st := newInboundStore(t)
	a := SbInbound{ServerID: 0, Type: "vless", Tag: "local", Listen: "::", ListenPort: 443, Options: "{}"}
	if _, err := st.SaveSbInbound(&a); err != nil {
		t.Fatal(err)
	}
	b := SbInbound{ServerID: 7, Type: "vless", Tag: "remote", Listen: "::", ListenPort: 443, Options: "{}"}
	if conflict, _, err := st.SbInboundPortConflict(&b); err != nil {
		t.Fatal(err)
	} else if conflict {
		t.Fatal("same port on a different server must not conflict")
	}
}

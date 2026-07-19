package sbstats

import (
	"strings"
	"testing"
)

// hugeLen builds a length varint with bit 63 set — the shape that made int(l)
// negative and slipped past every bounds check.
func hugeLen() []byte {
	b := []byte{}
	for i := 0; i < 9; i++ {
		b = append(b, 0xFF)
	}
	return append(b, 0x7F)
}

// The decoder is pointed at whatever is listening on sb_v2ray_listen — normally
// the local sing-box, but a buggy or hostile listener (or any process that binds
// 127.0.0.1:18080 before sing-box does) can reply with anything. Nothing in this
// package runs under a recover(), and the controller loop doesn't either, so a
// panic here takes down the whole panel process.
func TestDecodeQueryResponse_MalformedInputNeverPanics(t *testing.T) {
	cases := map[string][]byte{
		"stat length with bit 63 set":  append(append([]byte{0x0a}, hugeLen()...), 'x'),
		"name length with bit 63 set":  append(append([]byte{0x0a, 0x0c, 0x0a}, hugeLen()...), 'x'),
		"skipField length bit 63 set":  append(append([]byte{0x12}, hugeLen()...), 'x'),
		"truncated after tag":          {0x0a},
		"length exceeds buffer":        {0x0a, 0x40, 'a'},
		"varint that never terminates": {0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		"empty":                        {},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on malformed input: %v", r)
				}
			}()
			if _, err := decodeQueryResponse(in); err != nil {
				t.Logf("rejected: %v", err)
			}
		})
	}
}

// The 10-byte varint must not be allowed to set bit 63.
func TestReadVarint_RejectsOverflow(t *testing.T) {
	if v, n := readVarint(hugeLen()); n != 0 {
		t.Errorf("overflowing varint accepted: v=%d n=%d", v, n)
	}
	// A legitimate 10-byte varint (max uint64 has 0x01 as its 10th byte) still
	// decodes — only values that would wrap are refused.
	max := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}
	if v, n := readVarint(max); n != 10 || v != ^uint64(0) {
		t.Errorf("max uint64 varint = (%d, %d), want (%d, 10)", v, n, ^uint64(0))
	}
	// Sanity: ordinary small values are unaffected.
	if v, n := readVarint([]byte{0x08}); v != 8 || n != 1 {
		t.Errorf("small varint = (%d, %d), want (8, 1)", v, n)
	}
}

// A well-formed response must still decode — the hardening must not have broken
// the happy path.
func TestDecodeQueryResponse_WellFormed(t *testing.T) {
	stat := []byte{0x0a, byte(len("user>>>bob>>>traffic>>>uplink"))}
	stat = append(stat, "user>>>bob>>>traffic>>>uplink"...)
	stat = append(stat, 0x10, 0x80, 0x02) // value = 256

	msg := append([]byte{0x0a, byte(len(stat))}, stat...)
	out, err := decodeQueryResponse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := out["user>>>bob>>>traffic>>>uplink"]; got != 256 {
		t.Errorf("value = %d, want 256 (out=%v)", got, out)
	}
}

// splitName underpins the identity→bucket mapping; a name with the wrong shape
// must be dropped rather than mis-attributed.
func TestSplitNameShape(t *testing.T) {
	if got := splitName("user>>>bob>>>traffic>>>uplink"); len(got) != 4 || got[1] != "bob" {
		t.Errorf("splitName = %v", got)
	}
	if got := splitName("garbage"); len(got) != 1 {
		t.Errorf("splitName(garbage) = %v, want a single element", got)
	}
	if strings.Contains("", ">>>") {
		t.Fatal("unreachable")
	}
}

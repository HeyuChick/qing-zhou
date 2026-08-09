package subconv

import "testing"

func TestFormatForUA(t *testing.T) {
	cases := []struct {
		ua   string
		want string
	}{
		// The official sing-box clients identify by initials, not by "sing-box".
		{"SFA/1.12.0 (io.nekohasekai.sfa; sing-box 1.12.0)", FormatSingbox},
		{"SFI/1.11.0 (io.nekohasekai.sfa)", FormatSingbox},
		{"SFM/1.13.0", FormatSingbox},
		{"SFT/1.13.0", FormatSingbox},
		{"sing-box 1.13.4", FormatSingbox},
		{"singbox/1.12", FormatSingbox},

		{"clash-verge/v2.0.0", FormatClash},
		{"ClashforWindows/0.20.39", FormatClash},
		{"mihomo/1.18.0", FormatClash},
		{"Stash/2.7.0", FormatClash},
		// NekoBox is sing-box based but asks for Clash; the request wins.
		{"NekoBox/Android/1.3.6 (Prefer ClashMeta Format)", FormatClash},
		// …and asks for sing-box when it wants sing-box.
		{"NekoBox/Android/1.3.6 (Prefer sing-box Format)", FormatSingbox},

		{"Surge/2800 CFNetwork/1494 Darwin/23.4.0", FormatSurge},

		{"v2rayN/7.0", FormatBase64},
		{"Shadowrocket/2.2.32", FormatBase64},
		{"Quantumult%20X/1.0.30", FormatBase64},
		{"curl/8.7.1", FormatBase64},
		{"", FormatBase64},

		// The initials are matched as a prefix; an unrelated agent that merely
		// contains them must not be hijacked into a sing-box config.
		{"MySFIClient/1.0", FormatBase64},
		{"transfer-sft/2.0", FormatBase64},
	}
	for _, c := range cases {
		if got := FormatForUA(c.ua); got != c.want {
			t.Errorf("FormatForUA(%q) = %q, want %q", c.ua, got, c.want)
		}
	}
}

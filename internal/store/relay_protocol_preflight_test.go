package store

import "testing"

func TestRelayProtocolPreflightRejectsInvalidTLSAtomically(t *testing.T) {
	for _, protocol := range []string{"tuic", "hysteria", "hysteria2", "anytls"} {
		for _, side := range []int{0, 1} {
			t.Run(protocol+[]string{"-source", "-target"}[side], func(t *testing.T) {
				f := newUserMeteringFixture(t, 2)
				if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
					t.Fatal(err)
				}
				ib, err := f.st.GetSbInbound(f.inbounds[side])
				if err != nil {
					t.Fatal(err)
				}
				ib.Type, ib.TlsID, ib.Options = protocol, 0, `{}`
				if _, err = f.st.SaveSbInbound(ib); err != nil {
					t.Fatal(err)
				}
				if err = f.st.ConfigureTrafficMetering(true, true, true); err == nil {
					t.Fatal("TLS-required protocol enabled without TLS")
				}
				for _, setting := range []string{RelayUserMeteringSetting, "traffic_cumulative_metering"} {
					if value, err := f.st.GetSetting(setting); err != nil || value != "false" {
						t.Fatalf("failed preflight changed %s: %s %v", setting, value, err)
					}
				}
			})
		}
	}
}

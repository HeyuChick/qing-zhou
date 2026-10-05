package store

import (
	"slices"
	"testing"
)

func TestPreviewRelayVisionRequirementsBeforePerUserOptIn(t *testing.T) {
	f := visionCapabilityFixture(t, 2, `{}`)
	if _, err := f.st.DB().Exec(`UPDATE sb_inbounds SET server_id=0 WHERE id=?`, f.inbounds[0]); err != nil {
		t.Fatal(err)
	}
	if active, err := f.st.RelayUserVisionServerIDs(); err != nil || len(active) != 0 {
		t.Fatalf("existing enabled gate changed: %v %v", active, err)
	}
	ids, err := f.st.PreviewRelayUserVisionServerIDs()
	want := []int64{0, f.servers[1]}
	slices.Sort(want)
	if err != nil || !slices.Equal(ids, want) {
		t.Fatalf("pre-opt-in requirements %v want %v: %v", ids, want, err)
	}
	if f.st.RelayUserMeteringEnabled() {
		t.Fatal("preview enabled per-user metering")
	}
	if _, err = f.st.DB().Exec(`UPDATE sb_inbounds SET enabled=0 WHERE id=?`, f.inbounds[1]); err != nil {
		t.Fatal(err)
	}
	if ids, err = f.st.PreviewRelayUserVisionServerIDs(); err == nil || len(ids) > 0 {
		t.Fatalf("invalid topology produced guessed requirements: %v %v", ids, err)
	}
}

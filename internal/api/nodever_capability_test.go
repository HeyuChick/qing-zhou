package api

import (
	"testing"

	"qingzhou/internal/sbver"
	"qingzhou/internal/store"
)

func TestNodeVersionViewKeepsVisionCapabilitySeparate(t *testing.T) {
	for _, version := range []string{"", "1.14.2", "1.14.3", sbver.VisionFramingFixVersion} {
		fixed := sbver.HasVisionFramingFix(version)
		observed := map[int64]*store.NodeSingbox{0: {Version: version, HasVisionFramingFix: fixed, Error: "probe unavailable"}}
		view := viewFor(0, store.LocalNodeName, "", true, true, observed, nil)
		if view.HasVisionFramingFix != fixed || view.Error != "probe unavailable" || view.Version != version {
			t.Fatalf("capability/version/error presentation drift: %+v", view)
		}
		if version != "" && view.TooOld {
			t.Fatal("stock supported-version status must remain separate from P1 Vision capability")
		}
	}
}

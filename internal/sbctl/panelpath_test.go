package sbctl

import (
	"strings"
	"testing"

	"qingzhou/internal/store"
)

// TestPanelPathConflict covers the one deployment that restarts sing-box forever
// while every apply reports success: a server row that lands on the panel's own
// machine and installs to the same file the panel writes for server_id 0.
//
// Both writers no-op only on byte-identical content, so two writers with
// different content take turns overwriting each other — a restart per writer per
// sync pass, every minute, with a valid config on disk the whole time.
func TestPanelPathConflict(t *testing.T) {
	const panelPath = "/etc/sing-box/config.json"
	panelCfg := []byte(`{"inbounds":["all"]}`)

	t.Run("different content on the same file is refused", func(t *testing.T) {
		sv := &store.Server{ID: 3, Name: "本机复用", ConfigPath: panelPath}
		err := panelPathConflict(panelPath, sv, []byte(`{"inbounds":["some"]}`), panelCfg)
		if err == nil {
			t.Fatal("two writers on one file were allowed; the node would restart every pass")
		}
		// The operator has to be able to act on it, so the message has to name the
		// file and both ways out.
		for _, want := range []string{panelPath, "config_path"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error does not mention %q: %v", want, err)
			}
		}
	})

	t.Run("an implicit path is still the same file", func(t *testing.T) {
		// A row saved without a config path takes the same default applyLocal does,
		// which is exactly the panel's own path — the collision is the likelier one
		// precisely because nobody typed anything.
		sv := &store.Server{ID: 4, Name: "默认路径"}
		if err := panelPathConflict(panelPath, sv, []byte(`{"inbounds":["some"]}`), panelCfg); err == nil {
			t.Fatal("default config path was not recognised as the panel's own file")
		}
	})

	t.Run("identical content is the tidy setup, not a conflict", func(t *testing.T) {
		// Every inbound moved onto the row and nothing left at server_id 0: both
		// writers generate the same bytes, so neither ever rewrites the file. This
		// works today and must keep working.
		sv := &store.Server{ID: 5, Name: "全量迁移", ConfigPath: panelPath}
		if err := panelPathConflict(panelPath, sv, panelCfg, panelCfg); err != nil {
			t.Fatalf("identical configs rejected: %v", err)
		}
	})

	t.Run("a separate file is a separate node", func(t *testing.T) {
		sv := &store.Server{ID: 6, Name: "第二实例", ConfigPath: "/etc/sing-box/node2.json"}
		if err := panelPathConflict(panelPath, sv, []byte(`{"inbounds":["some"]}`), panelCfg); err != nil {
			t.Fatalf("a row with its own config file was rejected: %v", err)
		}
	})

	t.Run("nothing known about the panel's path means no opinion", func(t *testing.T) {
		sv := &store.Server{ID: 7, ConfigPath: panelPath}
		if err := panelPathConflict("", sv, []byte(`{"inbounds":["some"]}`), panelCfg); err != nil {
			t.Fatalf("guard fired without knowing the panel's own path: %v", err)
		}
		if err := panelPathConflict(panelPath, sv, []byte(`{"inbounds":["some"]}`), nil); err != nil {
			t.Fatalf("guard fired when the panel config failed to build: %v", err)
		}
	})
}

package subconv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRenderedConfigPassesMihomoCheck exercises the real parser when a local
// binary is explicitly supplied. In particular this catches unsupported proxy
// group/provider fields that yaml.Unmarshal-based unit tests cannot detect.
func TestRenderedConfigPassesMihomoCheck(t *testing.T) {
	bin := os.Getenv("QZ_MIHOMO_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_MIHOMO_TEST_BIN to a mihomo binary to run this")
	}
	proxies := ParseLinks(nodeLinks())
	proxies[0].AI = true
	out, err := Clash(proxies, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := exec.Command(bin, "-t", "-f", path, "-d", t.TempDir()).CombinedOutput()
	if err != nil {
		t.Fatalf("mihomo rejected the config we ship:\n%s", stripANSI(string(res)))
	}
}

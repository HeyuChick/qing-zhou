package subconv

import (
	"strings"
	"testing"
)

func TestRenderedDefaultHasNoEmptyDirectDetour(t *testing.T) {
	links := []string{
		"vless://a9c6a3ec-ef9b-4b88-8331-d2346b00a3a7@cdn.heyuchick.com:443?encryption=none&security=tls&sni=x&fp=chrome&type=ws&host=x&path=%2Fp#n1",
	}
	body, _, err := Render("singbox", links, nil, "", "", "https://example.com/sub/t")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(body, " ", ""), `"detour":"direct"`) {
		t.Fatalf("default singbox render still has detour->direct:\n%.500s", body)
	}
}

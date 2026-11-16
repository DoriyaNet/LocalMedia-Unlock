package outbound

import (
	"encoding/base64"
	"testing"
)

func TestParseSSURI(t *testing.T) {
	credentials := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret"))
	node, err := ParseURI("ss://" + credentials + "@example.com:8388#Test%20Node")
	if err != nil {
		t.Fatal(err)
	}
	if node.Type != "ss" || node.Server != "example.com" || node.Port != 8388 || node.Name != "Test Node" {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestParseVLESSRealityURI(t *testing.T) {
	node, err := ParseURI("vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=www.example.com&pbk=abc&sid=12&flow=xtls-rprx-vision&type=tcp#Reality")
	if err != nil {
		t.Fatal(err)
	}
	if !node.Reality || !node.TLS || node.PublicKey != "abc" || node.ShortID != "12" {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestParseSubscriptionBase64(t *testing.T) {
	lines := "vless://11111111-1111-1111-1111-111111111111@example.com:443?security=tls&sni=example.com#One\n" +
		"trojan://password@example.org:443#Two\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(lines))
	nodes, err := ParseSubscription(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Type != "vless" || nodes[1].Type != "trojan" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
}

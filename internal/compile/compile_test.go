package compile

import (
	"encoding/json"
	"strings"
	"testing"

	"localmedia-unlock/internal/model"
)

const testPublicKey = "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0"

type testConfigOptions struct {
	core      string
	hysteria2 bool
}

func testConfig(core string) model.Config {
	cfg := model.DefaultConfig()
	cfg.Settings.Core = core
	cfg.Settings.DNSMode = "remote"
	cfg.Settings.DNSServer = "1.1.1.1"
	cfg.Settings.ProxyAuthEnabled = true
	cfg.Settings.ProxyUsername = "alice"
	cfg.Settings.ProxyPassword = "proxy-secret"
	cfg.Settings.AdminToken = "do-not-emit-admin-token"
	cfg.Outbounds = []model.Outbound{
		{
			ID: "ss-1", Name: "ss-node", Type: "ss", Server: "ss.example.com", Port: 8388,
			Method: "aes-256-gcm", Password: "ss-secret", Network: "tcp", Plugin: "obfs",
			PluginOpts: map[string]string{"mode": "tls"}, Enabled: true,
		},
		{
			ID: "vmess-1", Name: "vmess-node", Type: "vmess", Server: "vmess.example.com",
			Port: 443, UUID: "11111111-1111-1111-1111-111111111111", Security: "auto",
			Network: "ws", TLS: true, SNI: "cdn.example.com", WSPath: "/socket",
			Fingerprint: "chrome", Enabled: true,
		},
		{
			ID: "vless-1", Name: "vless-node", Type: "vless", Server: "vless.example.com",
			Port: 443, UUID: "22222222-2222-2222-2222-222222222222", Network: "tcp",
			TLS: true, Reality: true, SNI: "www.example.com", PublicKey: testPublicKey,
			ShortID: "0123456789abcdef", Flow: "xtls-rprx-vision", Fingerprint: "chrome",
			Enabled: true,
		},
		{
			ID: "trojan-1", Name: "trojan-node", Type: "trojan", Server: "trojan.example.com",
			Port: 443, Password: "trojan-secret", Network: "grpc", TLS: true,
			SNI: "trojan.example.com", GRPCServiceName: "trojan-grpc", Enabled: true,
		},
		{
			ID: "hy2-1", Name: "hy2-node", Type: "hysteria2", Server: "hy2.example.com",
			Port: 443, Password: "hy2-secret", Network: "tcp", TLS: true,
			SNI: "hy2.example.com", UpMbps: 100, DownMbps: 500, Enabled: true,
		},
		{
			ID: "socks-1", Name: "socks-node", Type: "socks", Server: "127.0.0.1",
			Port: 1080, Username: "socks-user", Password: "socks-secret", Network: "tcp", Enabled: true,
		},
		{
			ID: "http-1", Name: "http-node", Type: "http", Server: "127.0.0.1",
			Port: 8080, Username: "http-user", Password: "http-secret", Network: "tcp", Enabled: true,
		},
		{
			ID: "disabled-1", Name: "disabled-node", Type: "vless", Server: "disabled.example.com",
			Port: 443, UUID: "33333333-3333-3333-3333-333333333333", Enabled: false,
		},
	}
	cfg.Routes = []model.Route{
		{Service: "chatgpt", Outbound: "vless-1", Enabled: true},
		{Service: "netflix", Outbound: "vmess-1", Enabled: true},
		{Service: "youtube", Outbound: "trojan-1", Enabled: true},
		{Service: "telegram", Outbound: "direct", Enabled: true},
	}
	cfg.CustomRoutes = []model.CustomRoute{{
		ID:       "custom-1",
		Domains:  []string{"full:exact.example.com", "keyword:keyword-value", "suffix:suffix.example.com", "bare.example.com"},
		Outbound: "socks-1",
		Enabled:  true,
	}}
	cfg.Inbounds = []model.Inbound{{
		ID: "mixed-main", Type: "mixed", Listen: "0.0.0.0", Port: 7890, Enabled: true,
	}}
	return cfg
}

func compileForTest(t *testing.T, cfg model.Config) map[string]any {
	t.Helper()
	var (
		data []byte
		err  error
	)
	switch cfg.Settings.Core {
	case model.CoreMihomo:
		data, err = CompileMihomo(cfg)
	case model.CoreSingBox:
		data, err = CompileSingBox(cfg)
	case model.CoreXray:
		data, err = CompileXray(cfg)
	default:
		t.Fatalf("unexpected core %q", cfg.Settings.Core)
	}
	if err != nil {
		t.Fatalf("compile %s: %v", cfg.Settings.Core, err)
	}
	if !json.Valid(data) {
		t.Fatalf("%s returned invalid JSON:\n%s", cfg.Settings.Core, data)
	}
	if strings.Contains(string(data), cfg.Settings.AdminToken) {
		t.Fatalf("%s leaked admin token", cfg.Settings.Core)
	}
	if strings.Contains(string(data), "external-controller") {
		t.Fatalf("%s must not publish the controller management endpoint", cfg.Settings.Core)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode %s configuration: %v", cfg.Settings.Core, err)
	}
	return result
}

func arraysAt(t *testing.T, root any, path ...string) []any {
	t.Helper()
	current := root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%q: parent is %T, want object", key, current)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("missing key %q", key)
		}
	}
	values, ok := current.([]any)
	if !ok {
		t.Fatalf("value is %T, want array", current)
	}
	return values
}

func objectAt(t *testing.T, root any, path ...string) map[string]any {
	t.Helper()
	current := root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%q: parent is %T, want object", key, current)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("missing key %q", key)
		}
	}
	object, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("value is %T, want object", current)
	}
	return object
}

func findByTag(t *testing.T, values []any, key, tag string) map[string]any {
	t.Helper()
	for _, value := range values {
		object, ok := value.(map[string]any)
		if ok && object[key] == tag {
			return object
		}
	}
	t.Fatalf("no entry with %s=%q", key, tag)
	return nil
}

func requireContains(t *testing.T, haystack []any, needle string) {
	t.Helper()
	for _, value := range haystack {
		if value == needle {
			return
		}
	}
	t.Fatalf("%q not found in %v", needle, haystack)
}

func TestCompileDispatchAndInputOrder(t *testing.T) {
	cfg := testConfig(model.CoreMihomo)
	cfg.Outbounds = append(cfg.Outbounds, model.Outbound{
		ID: "z-last", Name: "z-last", Type: "socks", Server: "127.0.0.1", Port: 1081, Enabled: true,
	})
	for _, core := range []string{model.CoreMihomo, model.CoreSingBox, model.CoreXray} {
		coreCfg := cfg
		coreCfg.Settings.Core = core
		if core == model.CoreXray {
			filtered := make([]model.Outbound, 0, len(cfg.Outbounds))
			for _, outbound := range cfg.Outbounds {
				if outbound.Type != "hysteria2" {
					outbound.Plugin = ""
					outbound.PluginOpts = nil
					filtered = append(filtered, outbound)
				}
			}
			coreCfg.Outbounds = filtered
		}
		data, err := Compile(coreCfg)
		if err != nil {
			t.Fatalf("%s: %v", core, err)
		}
		if !json.Valid(data) {
			t.Fatalf("%s produced invalid JSON", core)
		}
	}

	mihomo := compileForTest(t, testConfig(model.CoreMihomo))
	proxies := arraysAt(t, mihomo, "proxies")
	if len(proxies) != 7 {
		t.Fatalf("expected 7 enabled proxies, got %d", len(proxies))
	}
	if objectAt(t, proxies[0])["name"] != "ss-node" ||
		objectAt(t, proxies[len(proxies)-1])["name"] != "http-node" {
		t.Fatal("proxy order was not preserved")
	}
}

func TestMihomoInboundsAuthAndRouting(t *testing.T) {
	cfg := testConfig(model.CoreMihomo)
	cfg.Inbounds = append(cfg.Inbounds, model.Inbound{
		ID: "socks-in", Type: "socks", Listen: "127.0.0.1", Port: 7891, Enabled: true,
	})
	result := compileForTest(t, cfg)
	if result["mixed-port"] != float64(7890) || result["socks-port"] != float64(7891) {
		t.Fatalf("unexpected ports: %v", result)
	}
	auth := arraysAt(t, result, "authentication")
	if len(auth) != 1 || auth[0] != "alice:proxy-secret" {
		t.Fatalf("unexpected authentication: %v", auth)
	}
	rules := arraysAt(t, result, "rules")
	requireContains(t, rules, "DOMAIN-SUFFIX,netflix.com,vmess-node")
	requireContains(t, rules, "DOMAIN-SUFFIX,openai.com,vless-node")
	requireContains(t, rules, "DOMAIN,exact.example.com,socks-node")
	requireContains(t, rules, "DOMAIN-KEYWORD,keyword-value,socks-node")
	requireContains(t, rules, "DOMAIN-SUFFIX,suffix.example.com,socks-node")
	requireContains(t, rules, "DOMAIN-SUFFIX,bare.example.com,socks-node")
	if rules[len(rules)-1] != "MATCH,DIRECT" {
		t.Fatalf("expected final direct rule, got %q", rules[len(rules)-1])
	}
	proxy := findByTag(t, arraysAt(t, result, "proxies"), "name", "vless-node")
	reality := objectAt(t, proxy, "reality-opts")
	if reality["public-key"] != testPublicKey || reality["short-id"] != "0123456789abcdef" {
		t.Fatalf("REALITY mapping is wrong: %v", reality)
	}
	vmess := findByTag(t, arraysAt(t, result, "proxies"), "name", "vmess-node")
	if _, ok := vmess["alterId"]; !ok {
		t.Fatal("VMess alterId must be emitted")
	}
	if vmess["network"] != "ws" || objectAt(t, vmess, "ws-opts")["path"] != "/socket" {
		t.Fatalf("Mihomo WS mapping is wrong: %v", vmess)
	}
	trojan := findByTag(t, arraysAt(t, result, "proxies"), "name", "trojan-node")
	if trojan["sni"] != "trojan.example.com" || trojan["network"] != "grpc" {
		t.Fatalf("Mihomo Trojan TLS/gRPC mapping is wrong: %v", trojan)
	}
	if grpc := objectAt(t, trojan, "grpc-opts"); grpc["grpc-service-name"] != "trojan-grpc" {
		t.Fatalf("Mihomo gRPC service name is wrong: %v", grpc)
	}
	hy2 := findByTag(t, arraysAt(t, result, "proxies"), "name", "hy2-node")
	if hy2["sni"] != "hy2.example.com" || hy2["up"] != float64(100) || hy2["down"] != float64(500) {
		t.Fatalf("Mihomo Hysteria 2 mapping is wrong: %v", hy2)
	}
}

func TestMihomoAllowLANFalseBindsLoopback(t *testing.T) {
	cfg := testConfig(model.CoreMihomo)
	cfg.Settings.AllowLAN = false
	result := compileForTest(t, cfg)
	if result["bind-address"] != "127.0.0.1" {
		t.Fatalf("expected loopback bind address, got %v", result["bind-address"])
	}
}

func TestSingBoxProtocolsRealityAndRouting(t *testing.T) {
	result := compileForTest(t, testConfig(model.CoreSingBox))
	inbounds := arraysAt(t, result, "inbounds")
	mixed := findByTag(t, inbounds, "tag", "mixed-main")
	if mixed["type"] != "mixed" || mixed["listen"] != "0.0.0.0" || mixed["listen_port"] != float64(7890) {
		t.Fatalf("unexpected mixed inbound: %v", mixed)
	}
	users := arraysAt(t, mixed, "users")
	if len(users) != 1 {
		t.Fatalf("expected one inbound user, got %v", users)
	}
	user := objectAt(t, users[0])
	if user["username"] != "alice" || user[credentialField] != "proxy-secret" {
		t.Fatalf("unexpected inbound credentials: %v", user)
	}

	outbounds := arraysAt(t, result, "outbounds")
	direct := findByTag(t, outbounds, "tag", "direct")
	if direct["type"] != "direct" {
		t.Fatalf("direct outbound missing: %v", direct)
	}
	ss := findByTag(t, outbounds, "tag", "ss-node")
	if ss["type"] != "shadowsocks" {
		t.Fatalf("sing-box SS type should be shadowsocks: %v", ss)
	}
	vmess := findByTag(t, outbounds, "tag", "vmess-node")
	transport := objectAt(t, vmess, "transport")
	if transport["type"] != "ws" || transport["path"] != "/socket" {
		t.Fatalf("WS transport mapping is wrong: %v", transport)
	}
	vless := findByTag(t, outbounds, "tag", "vless-node")
	if vless["flow"] != "xtls-rprx-vision" {
		t.Fatalf("VLESS vision flow missing: %v", vless)
	}
	tls := objectAt(t, vless, "tls")
	reality := objectAt(t, tls, "reality")
	if reality["public_key"] != testPublicKey || reality["short_id"] != "0123456789abcdef" {
		t.Fatalf("REALITY mapping is wrong: %v", reality)
	}
	if tls["server_name"] != "www.example.com" {
		t.Fatalf("TLS server name missing: %v", tls)
	}
	trojan := findByTag(t, outbounds, "tag", "trojan-node")
	grpc := objectAt(t, trojan, "transport")
	if grpc["type"] != "grpc" || grpc["service_name"] != "trojan-grpc" {
		t.Fatalf("gRPC mapping is wrong: %v", grpc)
	}
	hy2 := findByTag(t, outbounds, "tag", "hy2-node")
	if hy2["up_mbps"] != float64(100) || hy2["down_mbps"] != float64(500) {
		t.Fatalf("Hysteria 2 bandwidth mapping is wrong: %v", hy2)
	}

	dns := objectAt(t, result, "dns")
	servers := arraysAt(t, dns, "servers")
	server := objectAt(t, servers[0])
	if server["type"] != "udp" || server["server"] != "1.1.1.1" {
		t.Fatalf("modern typed DNS server missing: %v", server)
	}
	if _, legacy := server["address"]; legacy {
		t.Fatalf("legacy DNS address emitted: %v", server)
	}

	rules := arraysAt(t, result, "route", "rules")
	netflixRule := findByTag(t, rules, "outbound", "vmess-node")
	requireContains(t, objectAt(t, netflixRule)["domain_suffix"].([]any), "netflix.com")
	route := objectAt(t, result, "route")
	if route["final"] != "direct" {
		t.Fatalf("expected route.final direct, got %v", route["final"])
	}
}

func TestSingBoxVLESSFlowNeedsTLS(t *testing.T) {
	cfg := testConfig(model.CoreSingBox)
	cfg.Outbounds = []model.Outbound{{
		ID: "vless-plain", Name: "vless-plain", Type: "vless", Server: "plain.example.com",
		Port: 443, UUID: "44444444-4444-4444-4444-444444444444", Flow: "xtls-rprx-vision",
		Network: "tcp", Enabled: true,
	}}
	cfg.Routes = []model.Route{{Service: "netflix", Outbound: "vless-plain", Enabled: true}}
	cfg.CustomRoutes = nil
	result := compileForTest(t, cfg)
	vless := findByTag(t, arraysAt(t, result, "outbounds"), "tag", "vless-plain")
	if _, ok := vless["flow"]; ok {
		t.Fatalf("VLESS flow must not be emitted without TLS/REALITY: %v", vless)
	}

	cfg = testConfig(model.CoreSingBox)
	cfg.Outbounds = []model.Outbound{{
		ID: "vless-ws", Name: "vless-ws", Type: "vless", Server: "ws.example.com",
		Port: 443, UUID: "55555555-5555-5555-5555-555555555555", Flow: "xtls-rprx-vision",
		Network: "ws", TLS: true, SNI: "ws.example.com", WSPath: "/ws", Enabled: true,
	}}
	cfg.Routes = []model.Route{{Service: "netflix", Outbound: "vless-ws", Enabled: true}}
	cfg.CustomRoutes = nil
	result = compileForTest(t, cfg)
	vless = findByTag(t, arraysAt(t, result, "outbounds"), "tag", "vless-ws")
	if _, ok := vless["flow"]; ok {
		t.Fatalf("VLESS flow must only be emitted with TCP: %v", vless)
	}
}

func TestXrayProtocolsInboundAndRouting(t *testing.T) {
	cfg := testConfig(model.CoreXray)
	cfg.Outbounds = cfg.Outbounds[:len(cfg.Outbounds)-1]
	filtered := cfg.Outbounds[:0]
	for _, outbound := range cfg.Outbounds {
		if outbound.Type != "hysteria2" {
			outbound.Plugin = ""
			outbound.PluginOpts = nil
			filtered = append(filtered, outbound)
		}
	}
	cfg.Outbounds = filtered
	result := compileForTest(t, cfg)

	inbounds := arraysAt(t, result, "inbounds")
	socks := findByTag(t, inbounds, "tag", "mixed-main-socks")
	httpInbound := findByTag(t, inbounds, "tag", "mixed-main-http")
	if socks["protocol"] != "socks" || socks["port"] != float64(7890) {
		t.Fatalf("unexpected socks inbound: %v", socks)
	}
	if httpInbound["protocol"] != "http" || httpInbound["port"] != float64(7891) {
		t.Fatalf("unexpected http inbound: %v", httpInbound)
	}
	socksSettings := objectAt(t, socks, "settings")
	if socksSettings["auth"] != credentialField || socksSettings["udp"] != true {
		t.Fatalf("socks auth/UDP mapping is wrong: %v", socksSettings)
	}
	accounts := arraysAt(t, socksSettings, "accounts")
	if len(accounts) != 1 || objectAt(t, accounts[0])["user"] != "alice" {
		t.Fatalf("unexpected Xray accounts: %v", accounts)
	}
	httpSettings := objectAt(t, httpInbound, "settings")
	httpAccounts := arraysAt(t, httpSettings, "accounts")
	if len(httpAccounts) != 1 || objectAt(t, httpAccounts[0])["pass"] != "proxy-secret" {
		t.Fatalf("unexpected Xray HTTP accounts: %v", httpAccounts)
	}

	outbounds := arraysAt(t, result, "outbounds")
	direct := findByTag(t, outbounds, "tag", "direct")
	if direct["protocol"] != "freedom" {
		t.Fatalf("direct freedom outbound missing: %v", direct)
	}
	vless := findByTag(t, outbounds, "tag", "vless-node")
	stream := objectAt(t, vless, "streamSettings")
	if stream["security"] != "reality" {
		t.Fatalf("VLESS REALITY security missing: %v", stream)
	}
	reality := objectAt(t, stream, "realitySettings")
	if reality["publicKey"] != testPublicKey || reality["shortId"] != "0123456789abcdef" {
		t.Fatalf("Xray REALITY mapping is wrong: %v", reality)
	}
	vmess := findByTag(t, outbounds, "tag", "vmess-node")
	vmessStream := objectAt(t, vmess, "streamSettings")
	if vmessStream["security"] != "tls" {
		t.Fatalf("VMess TLS mapping is wrong: %v", vmessStream)
	}
	ws := objectAt(t, vmessStream, "wsSettings")
	if ws["path"] != "/socket" {
		t.Fatalf("Xray WS mapping is wrong: %v", ws)
	}

	rules := arraysAt(t, result, "routing", "rules")
	var foundNetflix bool
	for _, value := range rules {
		rule := objectAt(t, value)
		if rule["outboundTag"] != "vmess-node" {
			continue
		}
		requireContains(t, rule["domain"].([]any), "domain:netflix.com")
		foundNetflix = true
	}
	if !foundNetflix {
		t.Fatal("missing netflix routing rule")
	}
	last := objectAt(t, rules[len(rules)-1])
	if last["network"] != "tcp,udp" || last["outboundTag"] != "direct" {
		t.Fatalf("missing Xray default catch-all rule: %v", last)
	}
}

func TestXrayHysteria2ReturnsError(t *testing.T) {
	cfg := testConfig(model.CoreXray)
	for index := range cfg.Outbounds {
		if cfg.Outbounds[index].Type == "ss" {
			cfg.Outbounds[index].Plugin = ""
			cfg.Outbounds[index].PluginOpts = nil
		}
	}
	_, err := CompileXray(cfg)
	if err == nil {
		t.Fatal("expected explicit hysteria2 error")
	}
	if !strings.Contains(err.Error(), "hysteria2") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCustomRoutePrefixes(t *testing.T) {
	for _, raw := range []string{
		"example.com", "domain:example.com", "suffix:example.com", "full:example.com", "keyword:example",
	} {
		rule, err := ParseDomainRule(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if rule.Value == "" {
			t.Fatalf("empty value for %q", raw)
		}
	}
	if rule, _ := ParseDomainRule("example.com"); rule.Kind != "suffix" {
		t.Fatalf("bare domain should be a suffix, got %q", rule.Kind)
	}
	if rule, _ := ParseDomainRule("domain:example.com"); rule.Kind != "suffix" {
		t.Fatalf("domain: should normalize to suffix, got %q", rule.Kind)
	}
	if _, err := ParseDomainRule("regex:.*"); err == nil {
		t.Fatal("regex should be rejected rather than emitted unsafely")
	}
	if _, err := ParseDomainRule("bad/domain"); err == nil {
		t.Fatal("invalid characters should be rejected")
	}
}

func TestRejectsUnsupportedTransportAndDisabledRouteTarget(t *testing.T) {
	for _, core := range []string{model.CoreMihomo, model.CoreSingBox, model.CoreXray} {
		cfg := model.DefaultConfig()
		cfg.Settings.Core = core
		cfg.Settings.ProxyAuthEnabled = false
		cfg.Outbounds = []model.Outbound{{
			ID: "ss-ws", Name: "ss-ws", Type: "ss", Server: "ss.example.com", Port: 8388,
			Method: "aes-256-gcm", Password: "secret", Network: "ws", Enabled: true,
		}}
		if _, err := Compile(cfg); err == nil {
			t.Fatalf("%s accepted WS for Shadowsocks", core)
		}

		cfg.Outbounds = []model.Outbound{{
			ID: "off", Name: "off", Type: "socks", Server: "127.0.0.1", Port: 1080,
			Network: "tcp", Enabled: false,
		}}
		cfg.Routes = []model.Route{{Service: "netflix", Outbound: "off", Enabled: true}}
		cfg.Normalize()
		if _, err := Compile(cfg); err == nil {
			t.Fatalf("%s accepted a route to a disabled outbound", core)
		}
	}
}

func TestLogLevelsAreMappedPerCore(t *testing.T) {
	cases := []struct {
		core string
		in   string
		want string
	}{
		{model.CoreMihomo, "warn", "warning"},
		{model.CoreMihomo, "silent", "silent"},
		{model.CoreSingBox, "silent", "fatal"},
		{model.CoreXray, "warn", "warning"},
		{model.CoreXray, "silent", "none"},
		{model.CoreXray, "info", "info"},
	}
	for _, item := range cases {
		if got := logLevelFor(item.core, item.in); got != item.want {
			t.Errorf("logLevelFor(%s, %s) = %q, want %q", item.core, item.in, got, item.want)
		}
	}
}

func TestDefaultOutboundIsDirect(t *testing.T) {
	for _, core := range []string{model.CoreMihomo, model.CoreSingBox, model.CoreXray} {
		cfg := model.DefaultConfig()
		cfg.Settings.Core = core
		cfg.Settings.ProxyAuthEnabled = false
		if err := cfg.Validate(); err != nil {
			t.Fatalf("default %s config: %v", core, err)
		}
		data, err := Compile(cfg)
		if err != nil {
			t.Fatalf("%s: %v", core, err)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("%s JSON: %v", core, err)
		}
		switch core {
		case model.CoreMihomo:
			rules := arraysAt(t, result, "rules")
			if rules[len(rules)-1] != "MATCH,DIRECT" {
				t.Fatalf("mihomo final rule: %v", rules[len(rules)-1])
			}
		case model.CoreSingBox:
			if objectAt(t, result, "route")["final"] != "direct" {
				t.Fatalf("singbox final: %v", objectAt(t, result, "route")["final"])
			}
		case model.CoreXray:
			outbounds := arraysAt(t, result, "outbounds")
			if len(outbounds) != 1 || objectAt(t, outbounds[0])["protocol"] != "freedom" {
				t.Fatalf("xray default outbounds: %v", outbounds)
			}
		}
	}
}

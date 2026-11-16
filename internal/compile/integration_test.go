package compile

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"localmedia-unlock/internal/model"
)

func TestCompileAllCoresIsValidJSON(t *testing.T) {
	fixtures := []model.Outbound{
		{ID: "ss", Name: "Tokyo SS", Type: "ss", Server: "ss.example.com", Port: 8388, Method: "aes-256-gcm", Password: "secret", Enabled: true, Network: "tcp"},
		{ID: "vm", Name: "US VMess", Type: "vmess", Server: "vm.example.com", Port: 443, UUID: "11111111-1111-1111-1111-111111111111", Network: "ws", TLS: true, SNI: "cdn.example.com", WSPath: "/ws", Enabled: true},
		{ID: "vl", Name: "JP VLESS", Type: "vless", Server: "vl.example.com", Port: 443, UUID: "22222222-2222-2222-2222-222222222222", Network: "tcp", TLS: true, Reality: true, SNI: "www.microsoft.com", PublicKey: "test-public-key", ShortID: "abcd", Flow: "xtls-rprx-vision", Fingerprint: "chrome", Enabled: true},
		{ID: "tr", Name: "SG Trojan", Type: "trojan", Server: "tr.example.com", Port: 443, Password: "secret", Network: "grpc", TLS: true, SNI: "tr.example.com", GRPCServiceName: "svc", Enabled: true},
	}
	for _, core := range []string{model.CoreMihomo, model.CoreSingBox, model.CoreXray} {
		t.Run(core, func(t *testing.T) {
			cfg := model.DefaultConfig()
			cfg.InitSecrets()
			cfg.Settings.Core = core
			cfg.Outbounds = fixtures
			for index := range cfg.Routes {
				cfg.Routes[index].Outbound = fixtures[index%len(fixtures)].ID
			}
			cfg.Normalize()
			data, err := Compile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(data) {
				t.Fatalf("generated %s config is not valid JSON", core)
			}
			if len(data) < 100 {
				t.Fatalf("generated %s config is unexpectedly short", core)
			}
		})
	}
}

func TestXrayRejectsHysteria2(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.InitSecrets()
	cfg.Settings.Core = model.CoreXray
	cfg.Outbounds = []model.Outbound{{
		ID: "hy", Name: "HY2", Type: "hysteria2", Server: "hy.example.com",
		Port: 443, Password: "secret", Enabled: true, Network: "tcp", TLS: true,
	}}
	cfg.Normalize()
	if _, err := Compile(cfg); err == nil {
		t.Fatal("expected Xray hysteria2 compile to fail")
	}
}

// TestKernelAcceptance runs real binaries when LOCALMEDIA_KERNEL_TEST=1 and
// LOCALMEDIA_*_BIN point at them. It is opt-in because the test suite must not
// download or execute software by itself.
func TestKernelAcceptance(t *testing.T) {
	if os.Getenv("LOCALMEDIA_KERNEL_TEST") != "1" {
		t.Skip("set LOCALMEDIA_KERNEL_TEST=1 to validate against installed kernels")
	}
	type kernel struct {
		core string
		env  string
		args func(string) []string
	}
	kernels := []kernel{
		{model.CoreMihomo, "LOCALMEDIA_MIHOMO_BIN", nil},
		{model.CoreSingBox, "LOCALMEDIA_SINGBOX_BIN", nil},
		{model.CoreXray, "LOCALMEDIA_XRAY_BIN", nil},
	}
	for _, item := range kernels {
		binary := os.Getenv(item.env)
		if binary == "" {
			binary = findTestKernel(item.core)
		}
		if binary == "" {
			t.Run(item.core, func(t *testing.T) {
				t.Skipf("%s not set and no local kernel found", item.env)
			})
			continue
		}
		if _, err := os.Stat(binary); err != nil {
			t.Run(item.core, func(t *testing.T) {
				t.Skipf("kernel %q unavailable: %v", binary, err)
			})
			continue
		}
		t.Run(item.core, func(t *testing.T) {
			cfg := testConfig(item.core)
			if item.core == model.CoreXray {
				filtered := make([]model.Outbound, 0, len(cfg.Outbounds))
				for _, outbound := range cfg.Outbounds {
					if outbound.Type != "hysteria2" {
						outbound.Plugin = ""
						outbound.PluginOpts = nil
						filtered = append(filtered, outbound)
					}
				}
				cfg.Outbounds = filtered
			}
			cfg.Settings.AllowLAN = false
			cfg.Inbounds[0].Port = freePort(t)
			cfg.Normalize()
			data, err := Compile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), item.core+".json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"-t", "-f", path, "-d", filepath.Dir(path)}
			if item.core == model.CoreSingBox {
				args = []string{"check", "-c", path}
			} else if item.core == model.CoreXray {
				args = []string{"run", "-test", "-c", path}
			}
			output, err := exec.Command(binary, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%s rejected config: %v\n%s\nconfig:\n%s", item.core, err, output, data)
			}
		})
	}
}

func findTestKernel(core string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	root := filepath.Join("..", "..", ".tmp", "kernels")
	switch core {
	case model.CoreMihomo:
		return filepath.Join(root, "mihomo", "mihomo-windows-amd64-compatible.exe")
	case model.CoreSingBox:
		return filepath.Join(root, "singbox", "sing-box-1.14.4-windows-amd64", "sing-box.exe")
	case model.CoreXray:
		return filepath.Join(root, "xray", "xray.exe")
	default:
		return ""
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := netListenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*netTCPAddr).Port
}

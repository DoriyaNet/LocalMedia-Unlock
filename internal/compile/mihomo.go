package compile

import (
	"fmt"
	"net"
	"strings"

	"localmedia-unlock/internal/model"
)

type mihomoConfig struct {
	MixedPort      int                `json:"mixed-port,omitempty"`
	Port           int                `json:"port,omitempty"`
	SocksPort      int                `json:"socks-port,omitempty"`
	Authentication []string           `json:"authentication,omitempty"`
	AllowLAN       bool               `json:"allow-lan"`
	BindAddress    string             `json:"bind-address,omitempty"`
	LogLevel       string             `json:"log-level"`
	IPv6           bool               `json:"ipv6"`
	DNS            mihomoDNS          `json:"dns"`
	Proxies        []mihomoProxy      `json:"proxies"`
	ProxyGroups    []mihomoProxyGroup `json:"proxy-groups"`
	Rules          []string           `json:"rules"`
}

type mihomoDNS struct {
	Enable       bool     `json:"enable"`
	IPv6         bool     `json:"ipv6"`
	EnhancedMode string   `json:"enhanced-mode,omitempty"`
	NameServer   []string `json:"nameserver"`
	Fallback     []string `json:"fallback,omitempty"`
}

type mihomoProxy struct {
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Server            string            `json:"server"`
	Port              int               `json:"port"`
	UUID              string            `json:"uuid,omitempty"`
	Password          string            `json:"-"`
	Cipher            string            `json:"cipher,omitempty"`
	Username          string            `json:"username,omitempty"`
	Network           string            `json:"network,omitempty"`
	TLS               bool              `json:"tls,omitempty"`
	SNI               string            `json:"servername,omitempty"`
	DirectSNI         string            `json:"sni,omitempty"`
	SkipCertVerify    bool              `json:"skip-cert-verify,omitempty"`
	ClientFingerprint string            `json:"client-fingerprint,omitempty"`
	Flow              string            `json:"flow,omitempty"`
	UDP               bool              `json:"udp,omitempty"`
	AlterID           *int              `json:"alterId,omitempty"`
	WSOpts            *mihomoWSOpts     `json:"ws-opts,omitempty"`
	GRPCOpts          *mihomoGRPCOpts   `json:"grpc-opts,omitempty"`
	RealityOpts       *mihomoReality    `json:"reality-opts,omitempty"`
	Plugin            string            `json:"plugin,omitempty"`
	PluginOpts        map[string]string `json:"plugin-opts,omitempty"`
	Up                int               `json:"up,omitempty"`
	Down              int               `json:"down,omitempty"`
}

func (proxy mihomoProxy) MarshalJSON() ([]byte, error) {
	type plain mihomoProxy
	return marshalWithCredential(plain(proxy), proxy.Password)
}

type mihomoWSOpts struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type mihomoGRPCOpts struct {
	GRPCServiceName string `json:"grpc-service-name"`
}

type mihomoReality struct {
	PublicKey string `json:"public-key"`
	ShortID   string `json:"short-id,omitempty"`
}

type mihomoProxyGroup struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Proxies []string `json:"proxies"`
}

// CompileMihomo renders a Mihomo/Clash Meta configuration.
func CompileMihomo(cfg model.Config) ([]byte, error) {
	byID, ids := outboundIndex(cfg)
	if err := validateRules(cfg, ids); err != nil {
		return nil, fmt.Errorf("mihomo: %w", err)
	}
	inbounds := enabledInbounds(cfg)
	if len(inbounds) == 0 {
		return nil, fmt.Errorf("mihomo: at least one inbound must be enabled")
	}
	if err := validateOutboundSet(cfg); err != nil {
		return nil, fmt.Errorf("mihomo: %w", err)
	}
	if err := validateInboundAuth(cfg, inbounds); err != nil {
		return nil, fmt.Errorf("mihomo: %w", err)
	}

	proxies := make([]mihomoProxy, 0, len(cfg.Outbounds))
	for _, outbound := range enabledOutbounds(cfg) {
		proxy, err := mihomoProxyFor(outbound)
		if err != nil {
			return nil, fmt.Errorf("mihomo: %w", err)
		}
		proxies = append(proxies, proxy)
	}

	groupNames := make([]string, 0, len(proxies)+1)
	for _, proxy := range proxies {
		groupNames = append(groupNames, proxy.Name)
	}
	groupNames = append(groupNames, "DIRECT")
	groups := []mihomoProxyGroup{{Name: "PROXY", Type: "select", Proxies: groupNames}}

	result := mihomoConfig{
		AllowLAN:    cfg.Settings.AllowLAN,
		LogLevel:    logLevelFor(model.CoreMihomo, cfg.Settings.LogLevel),
		IPv6:        true,
		Proxies:     proxies,
		ProxyGroups: groups,
		DNS:         mihomoDNSFor(cfg.Settings),
	}
	// The controller owns the management listen address. Leaving
	// external-controller unset avoids a port collision when the kernel starts.
	if !cfg.Settings.AllowLAN {
		result.BindAddress = "127.0.0.1"
	}
	if err := applyMihomoInbounds(&result, cfg, inbounds); err != nil {
		return nil, fmt.Errorf("mihomo: %w", err)
	}

	rules, err := mihomoRules(cfg, byID)
	if err != nil {
		return nil, fmt.Errorf("mihomo: %w", err)
	}
	result.Rules = rules
	return marshalConfig(result)
}

func applyMihomoInbounds(result *mihomoConfig, cfg model.Config, inbounds []model.Inbound) error {
	knownPorts := map[string]int{}
	for _, inbound := range inbounds {
		username, secret := inboundUsers(cfg.Settings, inbound)
		switch inbound.Type {
		case "mixed":
			if previous, ok := knownPorts["mixed"]; ok && previous != inbound.Port {
				return fmt.Errorf("multiple mixed inbounds on different ports are not supported by Mihomo top-level ports")
			}
			knownPorts["mixed"] = inbound.Port
			result.MixedPort = inbound.Port
		case "socks":
			if previous, ok := knownPorts["socks"]; ok && previous != inbound.Port {
				return fmt.Errorf("multiple socks inbounds on different ports are not supported by Mihomo top-level ports")
			}
			knownPorts["socks"] = inbound.Port
			result.SocksPort = inbound.Port
		case "http":
			if previous, ok := knownPorts["http"]; ok && previous != inbound.Port {
				return fmt.Errorf("multiple http inbounds on different ports are not supported by Mihomo top-level ports")
			}
			knownPorts["http"] = inbound.Port
			result.Port = inbound.Port
		}
		if username != "" && secret != "" {
			result.Authentication = append(result.Authentication, username+":"+secret)
		}
	}
	seen := map[string]bool{}
	unique := result.Authentication[:0]
	for _, auth := range result.Authentication {
		if !seen[auth] {
			seen[auth] = true
			unique = append(unique, auth)
		}
	}
	result.Authentication = unique
	return nil
}

func mihomoDNSFor(settings model.Settings) mihomoDNS {
	dns := mihomoDNS{Enable: true, IPv6: true, NameServer: []string{"system"}}
	if settings.DNSMode == "remote" {
		server := withDefault(strings.TrimSpace(settings.DNSServer), "1.1.1.1")
		dns.NameServer = []string{"https://" + net.JoinHostPort(server, "443") + "/dns-query"}
		dns.Fallback = []string{"tls://" + net.JoinHostPort(server, "853")}
	}
	return dns
}

func mihomoProxyFor(outbound model.Outbound) (mihomoProxy, error) {
	if strings.TrimSpace(outbound.Name) == "" {
		return mihomoProxy{}, fmt.Errorf("outbound %q has no name", outbound.ID)
	}
	if outbound.Server == "" || outbound.Port <= 0 {
		return mihomoProxy{}, fmt.Errorf("outbound %q requires server and port", outbound.Name)
	}
	if err := validateTransport(outbound); err != nil {
		return mihomoProxy{}, err
	}
	proxy := mihomoProxy{
		Name:   outbound.Name,
		Server: outbound.Server,
		Port:   outbound.Port,
		UDP:    true,
		Up:     outbound.UpMbps,
		Down:   outbound.DownMbps,
	}
	switch outbound.Type {
	case "ss":
		if outbound.Method == "" || outbound.Password == "" {
			return mihomoProxy{}, fmt.Errorf("outbound %q: Shadowsocks method and credentials are required", outbound.Name)
		}
		proxy.Type = "ss"
		proxy.Cipher = outbound.Method
		proxy.Password = outbound.Password
		proxy.Plugin = outbound.Plugin
		proxy.PluginOpts = outbound.PluginOpts
	case "vmess":
		if outbound.UUID == "" {
			return mihomoProxy{}, fmt.Errorf("outbound %q: VMess UUID is required", outbound.Name)
		}
		proxy.Type = "vmess"
		proxy.UUID = outbound.UUID
		proxy.Cipher = withDefault(outbound.Security, "auto")
		alterID := 0
		proxy.AlterID = &alterID
	case "vless":
		if outbound.UUID == "" {
			return mihomoProxy{}, fmt.Errorf("outbound %q: VLESS UUID is required", outbound.Name)
		}
		proxy.Type = "vless"
		proxy.UUID = outbound.UUID
		if (outbound.TLS || outbound.Reality) && outbound.Flow != "" && (outbound.Network == "" || outbound.Network == "tcp") {
			proxy.Flow = outbound.Flow
		}
	case "trojan":
		if outbound.Password == "" {
			return mihomoProxy{}, fmt.Errorf("outbound %q: Trojan credentials are required", outbound.Name)
		}
		proxy.Type = "trojan"
		proxy.Password = outbound.Password
	case "hysteria2":
		if outbound.Password == "" {
			return mihomoProxy{}, fmt.Errorf("outbound %q: Hysteria 2 credentials are required", outbound.Name)
		}
		proxy.Type = "hysteria2"
		proxy.Password = outbound.Password
		proxy.DirectSNI = withDefault(outbound.SNI, outbound.Server)
		proxy.SkipCertVerify = outbound.AllowInsecure
		proxy.Up = outbound.UpMbps
		proxy.Down = outbound.DownMbps
	case "socks":
		proxy.Type = "socks5"
		proxy.Username = outbound.Username
		proxy.Password = outbound.Password
		proxy.TLS = outbound.TLS
	case "http":
		proxy.Type = "http"
		proxy.Username = outbound.Username
		proxy.Password = outbound.Password
		proxy.TLS = outbound.TLS
	default:
		return mihomoProxy{}, fmt.Errorf("unsupported protocol %q", outbound.Type)
	}
	if outbound.Type == "vless" || outbound.Type == "vmess" || outbound.Type == "trojan" {
		if outbound.TLS {
			proxy.TLS = true
			if outbound.Type == "trojan" {
				proxy.DirectSNI = withDefault(outbound.SNI, outbound.Server)
			} else {
				proxy.SNI = withDefault(outbound.SNI, outbound.Server)
			}
			proxy.SkipCertVerify = outbound.AllowInsecure
			proxy.ClientFingerprint = outbound.Fingerprint
			if outbound.Fingerprint == "" && outbound.Reality {
				proxy.ClientFingerprint = "chrome"
			}
		}
		if outbound.Reality {
			if outbound.PublicKey == "" || outbound.SNI == "" {
				return mihomoProxy{}, fmt.Errorf("outbound %q: REALITY requires publicKey and sni", outbound.Name)
			}
			proxy.RealityOpts = &mihomoReality{PublicKey: outbound.PublicKey, ShortID: outbound.ShortID}
		}
	}
	if (outbound.Type == "socks" || outbound.Type == "http") && outbound.TLS {
		proxy.DirectSNI = withDefault(outbound.SNI, outbound.Server)
		proxy.SkipCertVerify = outbound.AllowInsecure
	}
	switch outbound.Network {
	case "ws":
		proxy.Network = "ws"
		proxy.WSOpts = &mihomoWSOpts{Path: withDefault(outbound.WSPath, "/")}
	case "grpc":
		proxy.Network = "grpc"
		proxy.GRPCOpts = &mihomoGRPCOpts{GRPCServiceName: outbound.GRPCServiceName}
	}
	return proxy, nil
}

func mihomoRules(cfg model.Config, byID map[string]model.Outbound) ([]string, error) {
	rules := make([]string, 0, 4*len(model.Presets)+len(cfg.CustomRoutes))
	for _, route := range enabledRoutes(cfg) {
		domains, err := presetDomains(route.Service)
		if err != nil {
			return nil, err
		}
		target := mihomoTarget(route.Outbound, byID)
		for _, domain := range domains {
			ruleType := "DOMAIN-SUFFIX"
			switch domain.Kind {
			case "full":
				ruleType = "DOMAIN"
			case "keyword":
				ruleType = "DOMAIN-KEYWORD"
			}
			rules = append(rules, ruleType+","+domain.Value+","+target)
		}
	}
	for _, route := range enabledCustomRoutes(cfg) {
		domains, err := compileDomainRules(route.Domains)
		if err != nil {
			return nil, fmt.Errorf("custom route %q: %w", route.ID, err)
		}
		target := mihomoTarget(route.Outbound, byID)
		for _, domain := range domains {
			ruleType := "DOMAIN-SUFFIX"
			switch domain.Kind {
			case "full":
				ruleType = "DOMAIN"
			case "keyword":
				ruleType = "DOMAIN-KEYWORD"
			}
			rules = append(rules, ruleType+","+domain.Value+","+target)
		}
	}
	defaultTarget := "DIRECT"
	if id := defaultOutboundID(cfg); id != model.DirectOutboundID {
		if outbound, ok := byID[id]; ok {
			defaultTarget = outbound.Name
		}
	}
	return append(rules, "MATCH,"+defaultTarget), nil
}

func mihomoTarget(id string, byID map[string]model.Outbound) string {
	if id == model.DirectOutboundID {
		return "DIRECT"
	}
	if outbound, ok := byID[id]; ok {
		return outbound.Name
	}
	return "DIRECT"
}

package compile

import (
	"fmt"
	"strings"

	"localmedia-unlock/internal/model"
)

type singBoxConfig struct {
	Log       singBoxLog        `json:"log"`
	DNS       singBoxDNS        `json:"dns"`
	Inbounds  []singBoxInbound  `json:"inbounds"`
	Outbounds []singBoxOutbound `json:"outbounds"`
	Route     singBoxRoute      `json:"route"`
}

type singBoxLog struct {
	Level string `json:"level"`
}

type singBoxDNS struct {
	Servers []singBoxDNSServer `json:"servers"`
}

type singBoxDNSServer struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
}

type singBoxInbound struct {
	Type       string        `json:"type"`
	Tag        string        `json:"tag"`
	Listen     string        `json:"listen"`
	ListenPort int           `json:"listen_port"`
	Users      []singBoxUser `json:"users,omitempty"`
}

type singBoxUser struct {
	Username string `json:"username"`
	Password string `json:"-"`
}

func (user singBoxUser) MarshalJSON() ([]byte, error) {
	type plain singBoxUser
	return marshalWithCredential(plain(user), user.Password)
}

type singBoxOutbound struct {
	Type       string            `json:"type"`
	Tag        string            `json:"tag"`
	Server     string            `json:"server,omitempty"`
	ServerPort int               `json:"server_port,omitempty"`
	Method     string            `json:"method,omitempty"`
	Password   string            `json:"-"`
	Username   string            `json:"username,omitempty"`
	UUID       string            `json:"uuid,omitempty"`
	Flow       string            `json:"flow,omitempty"`
	Network    string            `json:"network,omitempty"`
	TLS        *singBoxTLS       `json:"tls,omitempty"`
	Transport  *singBoxTransport `json:"transport,omitempty"`
	Plugin     string            `json:"plugin,omitempty"`
	PluginOpts string            `json:"plugin_opts,omitempty"`
	UpMbps     int               `json:"up_mbps,omitempty"`
	DownMbps   int               `json:"down_mbps,omitempty"`
}

func (outbound singBoxOutbound) MarshalJSON() ([]byte, error) {
	type plain singBoxOutbound
	return marshalWithCredential(plain(outbound), outbound.Password)
}

type singBoxTLS struct {
	Enabled    bool            `json:"enabled"`
	ServerName string          `json:"server_name,omitempty"`
	Insecure   bool            `json:"insecure,omitempty"`
	UTLS       *singBoxUTLS    `json:"utls,omitempty"`
	Reality    *singBoxReality `json:"reality,omitempty"`
}

type singBoxUTLS struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type singBoxReality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id,omitempty"`
}

type singBoxTransport struct {
	Type        string `json:"type"`
	Path        string `json:"path,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
}

type singBoxRoute struct {
	Rules               []singBoxRouteRule `json:"rules"`
	Final               string             `json:"final"`
	AutoDetectInterface bool               `json:"auto_detect_interface"`
}

type singBoxRouteRule struct {
	Domain        []string `json:"domain,omitempty"`
	DomainSuffix  []string `json:"domain_suffix,omitempty"`
	DomainKeyword []string `json:"domain_keyword,omitempty"`
	Outbound      string   `json:"outbound"`
}

// CompileSingBox renders a sing-box 1.11+ configuration. DNS uses the modern
// typed server form rather than the removed legacy address object.
func CompileSingBox(cfg model.Config) ([]byte, error) {
	byID, ids := outboundIndex(cfg)
	if err := validateRules(cfg, ids); err != nil {
		return nil, fmt.Errorf("singbox: %w", err)
	}
	inbounds := enabledInbounds(cfg)
	if len(inbounds) == 0 {
		return nil, fmt.Errorf("singbox: at least one inbound must be enabled")
	}
	if err := validateOutboundSet(cfg); err != nil {
		return nil, fmt.Errorf("singbox: %w", err)
	}
	if err := validateInboundAuth(cfg, inbounds); err != nil {
		return nil, fmt.Errorf("singbox: %w", err)
	}

	outbounds := []singBoxOutbound{{Type: "direct", Tag: "direct"}}
	for _, outbound := range enabledOutbounds(cfg) {
		rendered, err := singBoxOutboundFor(outbound)
		if err != nil {
			return nil, fmt.Errorf("singbox: %w", err)
		}
		outbounds = append(outbounds, rendered)
	}

	inboundConfigs := make([]singBoxInbound, 0, len(inbounds))
	for index, inbound := range inbounds {
		tag := strings.TrimSpace(inbound.ID)
		if tag == "" {
			tag = fmt.Sprintf("inbound-%d", index+1)
		}
		rendered := singBoxInbound{
			Type:       inbound.Type,
			Tag:        tag,
			Listen:     inboundAddress(cfg.Settings, inbound),
			ListenPort: inbound.Port,
		}
		username, secret := inboundUsers(cfg.Settings, inbound)
		if username != "" && secret != "" {
			rendered.Users = []singBoxUser{{Username: username, Password: secret}}
		}
		inboundConfigs = append(inboundConfigs, rendered)
	}

	rules, err := singBoxRules(cfg, byID)
	if err != nil {
		return nil, fmt.Errorf("singbox: %w", err)
	}
	result := singBoxConfig{
		Log:       singBoxLog{Level: logLevelFor(model.CoreSingBox, cfg.Settings.LogLevel)},
		DNS:       singBoxDNSFor(cfg.Settings),
		Inbounds:  inboundConfigs,
		Outbounds: outbounds,
		Route:     singBoxRoute{Rules: rules, Final: defaultOutboundID(cfg), AutoDetectInterface: true},
	}
	return marshalConfig(result)
}

func singBoxDNSFor(settings model.Settings) singBoxDNS {
	if settings.DNSMode != "remote" {
		return singBoxDNS{Servers: []singBoxDNSServer{{Type: "local", Tag: "local"}}}
	}
	return singBoxDNS{Servers: []singBoxDNSServer{{
		Type:       "udp",
		Tag:        "remote",
		Server:     withDefault(strings.TrimSpace(settings.DNSServer), "1.1.1.1"),
		ServerPort: 53,
	}}}
}

func singBoxOutboundFor(outbound model.Outbound) (singBoxOutbound, error) {
	if outbound.Name == "" || outbound.Server == "" || outbound.Port <= 0 {
		return singBoxOutbound{}, fmt.Errorf("outbound %q requires name, server and port", outbound.ID)
	}
	if err := validateTransport(outbound); err != nil {
		return singBoxOutbound{}, err
	}
	coreType := outbound.Type
	if coreType == "ss" {
		coreType = "shadowsocks"
	}
	result := singBoxOutbound{
		Type:       coreType,
		Tag:        outbound.Name,
		Server:     outbound.Server,
		ServerPort: outbound.Port,
	}
	switch outbound.Type {
	case "ss":
		if outbound.Method == "" || outbound.Password == "" {
			return singBoxOutbound{}, fmt.Errorf("outbound %q: Shadowsocks method and credentials are required", outbound.Name)
		}
		result.Method = outbound.Method
		result.Password = outbound.Password
		if outbound.Plugin != "" {
			plugin := outbound.Plugin
			if plugin == "obfs" {
				plugin = "obfs-local"
			}
			switch plugin {
			case "obfs-local", "v2ray-plugin":
			default:
				return singBoxOutbound{}, fmt.Errorf("outbound %q: Shadowsocks plugin %q is not built into sing-box", outbound.Name, outbound.Plugin)
			}
			result.Plugin = plugin
			result.PluginOpts = pluginOptionsString(outbound.PluginOpts)
		}
	case "vmess":
		if outbound.UUID == "" {
			return singBoxOutbound{}, fmt.Errorf("outbound %q: VMess UUID is required", outbound.Name)
		}
		result.UUID = outbound.UUID
	case "vless":
		if outbound.UUID == "" {
			return singBoxOutbound{}, fmt.Errorf("outbound %q: VLESS UUID is required", outbound.Name)
		}
		result.UUID = outbound.UUID
		if (outbound.TLS || outbound.Reality) && outbound.Flow != "" && (outbound.Network == "" || outbound.Network == "tcp") {
			result.Flow = outbound.Flow
		}
	case "trojan":
		if outbound.Password == "" {
			return singBoxOutbound{}, fmt.Errorf("outbound %q: Trojan credentials are required", outbound.Name)
		}
		result.Password = outbound.Password
	case "hysteria2":
		if outbound.Password == "" {
			return singBoxOutbound{}, fmt.Errorf("outbound %q: Hysteria 2 credentials are required", outbound.Name)
		}
		result.Password = outbound.Password
		result.UpMbps = outbound.UpMbps
		result.DownMbps = outbound.DownMbps
	case "socks", "http":
		result.Username = outbound.Username
		result.Password = outbound.Password
	default:
		return singBoxOutbound{}, fmt.Errorf("unsupported protocol %q", outbound.Type)
	}
	if outbound.TLS || outbound.Reality || outbound.Type == "hysteria2" {
		tls := &singBoxTLS{Enabled: true, ServerName: withDefault(outbound.SNI, outbound.Server), Insecure: outbound.AllowInsecure}
		if outbound.Reality && (outbound.PublicKey == "" || outbound.SNI == "") {
			return singBoxOutbound{}, fmt.Errorf("outbound %q: REALITY requires publicKey and sni", outbound.Name)
		}
		if outbound.Reality {
			tls.UTLS = &singBoxUTLS{Enabled: true, Fingerprint: withDefault(outbound.Fingerprint, "chrome")}
		} else if outbound.Fingerprint != "" && supportsV2RayTransport(outbound.Type) {
			tls.UTLS = &singBoxUTLS{Enabled: true, Fingerprint: outbound.Fingerprint}
		}
		if outbound.Reality {
			tls.Reality = &singBoxReality{Enabled: true, PublicKey: outbound.PublicKey, ShortID: outbound.ShortID}
		}
		result.TLS = tls
	}
	switch outbound.Network {
	case "ws":
		result.Transport = &singBoxTransport{Type: "ws", Path: withDefault(outbound.WSPath, "/")}
	case "grpc":
		result.Transport = &singBoxTransport{Type: "grpc", ServiceName: outbound.GRPCServiceName}
	}
	return result, nil
}

func singBoxRules(cfg model.Config, byID map[string]model.Outbound) ([]singBoxRouteRule, error) {
	rules := make([]singBoxRouteRule, 0, len(model.Presets)+len(cfg.CustomRoutes))
	for _, route := range enabledRoutes(cfg) {
		domains, err := presetDomains(route.Service)
		if err != nil {
			return nil, err
		}
		rule := singBoxRouteRule{Outbound: singBoxTarget(route.Outbound, byID)}
		appendSingBoxDomains(&rule, domains)
		rules = append(rules, rule)
	}
	for _, route := range enabledCustomRoutes(cfg) {
		domains, err := compileDomainRules(route.Domains)
		if err != nil {
			return nil, fmt.Errorf("custom route %q: %w", route.ID, err)
		}
		rule := singBoxRouteRule{Outbound: singBoxTarget(route.Outbound, byID)}
		appendSingBoxDomains(&rule, domains)
		rules = append(rules, rule)
	}
	return rules, nil
}

func appendSingBoxDomains(rule *singBoxRouteRule, domains []domainRule) {
	for _, domain := range domains {
		switch domain.Kind {
		case "full":
			rule.Domain = append(rule.Domain, domain.Value)
		case "keyword":
			rule.DomainKeyword = append(rule.DomainKeyword, domain.Value)
		default:
			rule.DomainSuffix = append(rule.DomainSuffix, domain.Value)
		}
	}
}

func singBoxTarget(id string, byID map[string]model.Outbound) string {
	if id == model.DirectOutboundID {
		return "direct"
	}
	if outbound, ok := byID[id]; ok {
		return outbound.Name
	}
	return "direct"
}

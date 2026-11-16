package compile

import (
	"fmt"
	"net"
	"strings"

	"localmedia-unlock/internal/model"
)

type xrayConfig struct {
	Log       xrayLog        `json:"log"`
	DNS       *xrayDNS       `json:"dns,omitempty"`
	Inbounds  []xrayInbound  `json:"inbounds"`
	Outbounds []xrayOutbound `json:"outbounds"`
	Routing   xrayRouting    `json:"routing"`
}

type xrayLog struct {
	LogLevel string `json:"loglevel"`
}

type xrayDNS struct {
	Servers []string `json:"servers"`
}

type xrayInbound struct {
	Tag      string               `json:"tag"`
	Listen   string               `json:"listen"`
	Port     int                  `json:"port"`
	Protocol string               `json:"protocol"`
	Settings *xrayInboundSettings `json:"settings,omitempty"`
	Sniffing *xraySniffing        `json:"sniffing,omitempty"`
}

type xrayInboundSettings struct {
	Auth     string        `json:"auth,omitempty"`
	Accounts []xrayAccount `json:"accounts,omitempty"`
	UDP      bool          `json:"udp,omitempty"`
}

type xrayAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type xraySniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	RouteOnly    bool     `json:"routeOnly"`
}

type xrayOutbound struct {
	Tag            string              `json:"tag"`
	Protocol       string              `json:"protocol"`
	Settings       any                 `json:"settings,omitempty"`
	StreamSettings *xrayStreamSettings `json:"streamSettings,omitempty"`
}

type xrayStreamSettings struct {
	Network  string       `json:"network,omitempty"`
	Security string       `json:"security,omitempty"`
	TLS      *xrayTLS     `json:"tlsSettings,omitempty"`
	Reality  *xrayReality `json:"realitySettings,omitempty"`
	WS       *xrayWS      `json:"wsSettings,omitempty"`
	GRPC     *xrayGRPC    `json:"grpcSettings,omitempty"`
}

type xrayTLS struct {
	ServerName    string `json:"serverName,omitempty"`
	AllowInsecure bool   `json:"allowInsecure,omitempty"`
}

type xrayReality struct {
	ServerName  string `json:"serverName,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type xrayWS struct {
	Path string `json:"path,omitempty"`
}

type xrayGRPC struct {
	ServiceName string `json:"serviceName,omitempty"`
}

type xrayRouting struct {
	DomainStrategy string     `json:"domainStrategy"`
	Rules          []xrayRule `json:"rules"`
}

type xrayRule struct {
	Type        string   `json:"type"`
	Domain      []string `json:"domain,omitempty"`
	Network     string   `json:"network,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// CompileXray renders an Xray configuration. Hysteria 2 has no Xray protocol
// equivalent, so its presence is a hard error rather than a broken fallback.
func CompileXray(cfg model.Config) ([]byte, error) {
	byID, ids := outboundIndex(cfg)
	if err := validateRules(cfg, ids); err != nil {
		return nil, fmt.Errorf("xray: %w", err)
	}
	inbounds := enabledInbounds(cfg)
	if len(inbounds) == 0 {
		return nil, fmt.Errorf("xray: at least one inbound must be enabled")
	}
	if err := validateOutboundSet(cfg); err != nil {
		return nil, fmt.Errorf("xray: %w", err)
	}
	if err := validateInboundAuth(cfg, inbounds); err != nil {
		return nil, fmt.Errorf("xray: %w", err)
	}

	outbounds := []xrayOutbound{{Tag: "direct", Protocol: "freedom"}}
	for _, outbound := range enabledOutbounds(cfg) {
		if outbound.Type == "hysteria2" {
			return nil, fmt.Errorf("xray: protocol hysteria2 is not supported by Xray (outbound %q)", outbound.Name)
		}
		rendered, err := xrayOutboundFor(outbound)
		if err != nil {
			return nil, fmt.Errorf("xray: %w", err)
		}
		outbounds = append(outbounds, rendered)
	}

	renderedInbounds, err := xrayInbounds(cfg, inbounds)
	if err != nil {
		return nil, fmt.Errorf("xray: %w", err)
	}
	rules, err := xrayRules(cfg, byID)
	if err != nil {
		return nil, fmt.Errorf("xray: %w", err)
	}
	result := xrayConfig{
		Log:       xrayLog{LogLevel: logLevelFor(model.CoreXray, cfg.Settings.LogLevel)},
		Inbounds:  renderedInbounds,
		Outbounds: outbounds,
		Routing:   xrayRouting{DomainStrategy: "AsIs", Rules: rules},
	}
	if cfg.Settings.DNSMode == "remote" {
		server := withDefault(strings.TrimSpace(cfg.Settings.DNSServer), "1.1.1.1")
		result.DNS = &xrayDNS{Servers: []string{"udp:" + net.JoinHostPort(server, "53")}}
	}
	return marshalConfig(result)
}

func xrayInbounds(cfg model.Config, inbounds []model.Inbound) ([]xrayInbound, error) {
	result := make([]xrayInbound, 0, len(inbounds)*2)
	ports := make(map[string]string)
	for index, inbound := range inbounds {
		baseTag := strings.TrimSpace(inbound.ID)
		if baseTag == "" {
			baseTag = fmt.Sprintf("inbound-%d", index+1)
		}
		listen := inboundAddress(cfg.Settings, inbound)
		protocols := []string{inbound.Type}
		if inbound.Type == "mixed" {
			protocols = []string{"socks", "http"}
		}
		for protocolIndex, protocol := range protocols {
			port := inbound.Port
			tag := baseTag
			if inbound.Type == "mixed" {
				port += protocolIndex
				tag = baseTag + "-" + protocol
				if protocolIndex > 0 && port > 65535 {
					return nil, fmt.Errorf("inbound %q: HTTP companion port exceeds 65535", inbound.ID)
				}
			}
			key := net.JoinHostPort(listen, portString(port))
			if previous, ok := ports[key]; ok {
				return nil, fmt.Errorf("inbound %q on %s conflicts with %s; Xray requires distinct socks/http ports", inbound.ID, key, previous)
			}
			ports[key] = tag
			entry := xrayInbound{
				Tag:      tag,
				Listen:   listen,
				Port:     port,
				Protocol: protocol,
				Sniffing: &xraySniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}, RouteOnly: true},
			}
			username, secret := inboundUsers(cfg.Settings, inbound)
			if protocol == "socks" {
				entry.Settings = &xrayInboundSettings{Auth: "noauth", UDP: true}
				if username != "" && secret != "" {
					entry.Settings = &xrayInboundSettings{Auth: credentialField, Accounts: []xrayAccount{{User: username, Pass: secret}}, UDP: true}
				}
			} else {
				entry.Settings = &xrayInboundSettings{}
				if username != "" && secret != "" {
					entry.Settings.Accounts = []xrayAccount{{User: username, Pass: secret}}
				}
			}
			result = append(result, entry)
		}
	}
	return result, nil
}

func xrayOutboundFor(outbound model.Outbound) (xrayOutbound, error) {
	if outbound.Name == "" || outbound.Server == "" || outbound.Port <= 0 {
		return xrayOutbound{}, fmt.Errorf("outbound %q requires name, server and port", outbound.ID)
	}
	if err := validateTransport(outbound); err != nil {
		return xrayOutbound{}, err
	}
	result := xrayOutbound{Tag: outbound.Name}
	switch outbound.Type {
	case "ss":
		if outbound.Method == "" || outbound.Password == "" {
			return xrayOutbound{}, fmt.Errorf("outbound %q: Shadowsocks method and credentials are required", outbound.Name)
		}
		if outbound.Plugin != "" {
			return xrayOutbound{}, fmt.Errorf("outbound %q: Shadowsocks plugin %q is not supported by Xray", outbound.Name, outbound.Plugin)
		}
		result.Protocol = "shadowsocks"
		result.Settings = map[string]any{
			"servers": []map[string]any{{
				"address":       outbound.Server,
				"port":          outbound.Port,
				"method":        outbound.Method,
				credentialField: outbound.Password,
			}},
		}
	case "vmess":
		if outbound.UUID == "" {
			return xrayOutbound{}, fmt.Errorf("outbound %q: VMess UUID is required", outbound.Name)
		}
		result.Protocol = "vmess"
		result.Settings = map[string]any{
			"vnext": []map[string]any{{
				"address": outbound.Server,
				"port":    outbound.Port,
				"users": []map[string]any{{
					"id":         outbound.UUID,
					"security":   withDefault(outbound.Security, "auto"),
					"alterId":    0,
					"encryption": "auto",
				}},
			}},
		}
	case "vless":
		if outbound.UUID == "" {
			return xrayOutbound{}, fmt.Errorf("outbound %q: VLESS UUID is required", outbound.Name)
		}
		result.Protocol = "vless"
		user := map[string]any{"id": outbound.UUID, "encryption": "none"}
		if outbound.Flow != "" {
			if !outbound.TLS && !outbound.Reality {
				return xrayOutbound{}, fmt.Errorf("outbound %q: VLESS flow requires TLS or REALITY", outbound.Name)
			}
			if outbound.Network != "" && outbound.Network != "tcp" {
				return xrayOutbound{}, fmt.Errorf("outbound %q: VLESS flow requires TCP transport", outbound.Name)
			}
			user["flow"] = outbound.Flow
		}
		result.Settings = map[string]any{
			"vnext": []map[string]any{{
				"address": outbound.Server,
				"port":    outbound.Port,
				"users":   []map[string]any{user},
			}},
		}
	case "trojan":
		if outbound.Password == "" {
			return xrayOutbound{}, fmt.Errorf("outbound %q: Trojan credentials are required", outbound.Name)
		}
		result.Protocol = "trojan"
		result.Settings = map[string]any{
			"servers": []map[string]any{{
				"address":       outbound.Server,
				"port":          outbound.Port,
				credentialField: outbound.Password,
			}},
		}
	case "socks":
		result.Protocol = "socks"
		server := map[string]any{"address": outbound.Server, "port": outbound.Port}
		if outbound.Username != "" || outbound.Password != "" {
			server["users"] = []map[string]any{{"user": outbound.Username, "pass": outbound.Password}}
		}
		result.Settings = map[string]any{"servers": []map[string]any{server}}
	case "http":
		result.Protocol = "http"
		server := map[string]any{"address": outbound.Server, "port": outbound.Port}
		if outbound.Username != "" || outbound.Password != "" {
			server["users"] = []map[string]any{{"user": outbound.Username, "pass": outbound.Password}}
		}
		result.Settings = map[string]any{"servers": []map[string]any{server}}
	default:
		return xrayOutbound{}, fmt.Errorf("unsupported protocol %q", outbound.Type)
	}

	stream, err := xrayStreamFor(outbound)
	if err != nil {
		return xrayOutbound{}, err
	}
	result.StreamSettings = stream
	return result, nil
}

func xrayStreamFor(outbound model.Outbound) (*xrayStreamSettings, error) {
	stream := &xrayStreamSettings{Network: withDefault(outbound.Network, "tcp")}
	if outbound.TLS || outbound.Reality {
		if outbound.Reality {
			if outbound.PublicKey == "" || outbound.SNI == "" {
				return nil, fmt.Errorf("outbound %q: REALITY requires publicKey and sni", outbound.Name)
			}
			stream.Security = "reality"
			stream.Reality = &xrayReality{
				ServerName:  outbound.SNI,
				PublicKey:   outbound.PublicKey,
				ShortID:     outbound.ShortID,
				Fingerprint: withDefault(outbound.Fingerprint, "chrome"),
			}
		} else {
			stream.Security = "tls"
			stream.TLS = &xrayTLS{
				ServerName:    outbound.SNI,
				AllowInsecure: outbound.AllowInsecure,
			}
		}
	}
	switch outbound.Network {
	case "ws":
		stream.WS = &xrayWS{Path: withDefault(outbound.WSPath, "/")}
	case "grpc":
		stream.GRPC = &xrayGRPC{ServiceName: outbound.GRPCServiceName}
	}
	return stream, nil
}

func xrayRules(cfg model.Config, byID map[string]model.Outbound) ([]xrayRule, error) {
	rules := make([]xrayRule, 0, len(model.Presets)+len(cfg.CustomRoutes))
	for _, route := range enabledRoutes(cfg) {
		domains, err := presetDomains(route.Service)
		if err != nil {
			return nil, err
		}
		rule := xrayRule{Type: "field", OutboundTag: xrayTarget(route.Outbound, byID)}
		appendXrayDomains(&rule, domains)
		rules = append(rules, rule)
	}
	for _, route := range enabledCustomRoutes(cfg) {
		domains, err := compileDomainRules(route.Domains)
		if err != nil {
			return nil, fmt.Errorf("custom route %q: %w", route.ID, err)
		}
		rule := xrayRule{Type: "field", OutboundTag: xrayTarget(route.Outbound, byID)}
		appendXrayDomains(&rule, domains)
		rules = append(rules, rule)
	}
	rules = append(rules, xrayRule{
		Type:        "field",
		Network:     "tcp,udp",
		OutboundTag: xrayTarget(defaultOutboundID(cfg), byID),
	})
	return rules, nil
}

func appendXrayDomains(rule *xrayRule, domains []domainRule) {
	for _, domain := range domains {
		switch domain.Kind {
		case "full":
			rule.Domain = append(rule.Domain, "full:"+domain.Value)
		case "keyword":
			rule.Domain = append(rule.Domain, "keyword:"+domain.Value)
		default:
			rule.Domain = append(rule.Domain, "domain:"+domain.Value)
		}
	}
}

func xrayTarget(id string, byID map[string]model.Outbound) string {
	if id == model.DirectOutboundID {
		return "direct"
	}
	if outbound, ok := byID[id]; ok {
		return outbound.Name
	}
	return "direct"
}

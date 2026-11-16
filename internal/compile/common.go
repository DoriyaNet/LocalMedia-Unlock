// Package compile renders the controller's portable configuration into
// native configurations for Mihomo, sing-box and Xray.
package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"localmedia-unlock/internal/model"
)

// Compile dispatches on cfg.Settings.Core and returns a stable, indented JSON
// document without comments.
func Compile(cfg model.Config) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Settings.Core)) {
	case "", model.CoreMihomo:
		return CompileMihomo(cfg)
	case model.CoreSingBox:
		return CompileSingBox(cfg)
	case model.CoreXray:
		return CompileXray(cfg)
	default:
		return nil, fmt.Errorf("compile: unsupported core %q", cfg.Settings.Core)
	}
}

// domainRule is the normalized form shared by all three renderers.
type domainRule struct {
	Kind  string
	Value string
}

// ParseDomainRule normalizes the controller rule syntax. Values without an
// explicit prefix match the domain itself and all subdomains.
func ParseDomainRule(raw string) (domainRule, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return domainRule{}, fmt.Errorf("empty domain rule")
	}
	kind := "suffix"
	value := raw
	if index := strings.IndexByte(raw, ':'); index >= 0 {
		prefix := raw[:index]
		switch prefix {
		case "domain", "suffix", "full", "keyword":
			kind = prefix
			value = raw[index+1:]
			if kind == "domain" {
				kind = "suffix"
			}
		case "regex", "regexp", "geosite", "geoip":
			return domainRule{}, fmt.Errorf("unsupported domain rule prefix %q", prefix)
		default:
			return domainRule{}, fmt.Errorf("unknown domain rule prefix %q", prefix)
		}
	}
	value = strings.TrimSuffix(value, ".")
	value = strings.TrimPrefix(value, ".")
	if value == "" {
		return domainRule{}, fmt.Errorf("empty domain in rule %q", raw)
	}
	if strings.ContainsAny(value, " \t\r\n,/") {
		return domainRule{}, fmt.Errorf("invalid domain in rule %q", raw)
	}
	if kind != "keyword" && (value == "localhost" || value == "localhost:") {
		return domainRule{}, fmt.Errorf("invalid domain in rule %q", raw)
	}
	return domainRule{Kind: kind, Value: value}, nil
}

func compileDomainRules(domains []string) ([]domainRule, error) {
	rules := make([]domainRule, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, domain := range domains {
		rule, err := ParseDomainRule(domain)
		if err != nil {
			return nil, err
		}
		key := rule.Kind + "\x00" + rule.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		rules = append(rules, rule)
	}
	return rules, nil
}

func validateRules(cfg model.Config, outboundIDs map[string]bool) error {
	defaultID := defaultOutboundID(cfg)
	if !outboundIDs[defaultID] {
		return fmt.Errorf("default outbound %q is missing or disabled", defaultID)
	}
	seenServices := make(map[string]bool, len(cfg.Routes))
	for _, route := range cfg.Routes {
		if !route.Enabled {
			continue
		}
		if _, ok := model.PresetByID(route.Service); !ok {
			return fmt.Errorf("route: unknown service %q", route.Service)
		}
		if seenServices[route.Service] {
			return fmt.Errorf("route: duplicate service %q", route.Service)
		}
		seenServices[route.Service] = true
		if !outboundIDs[route.Outbound] {
			return fmt.Errorf("route %q: outbound %q is missing or disabled", route.Service, route.Outbound)
		}
	}
	for _, route := range cfg.CustomRoutes {
		if !route.Enabled {
			continue
		}
		if !outboundIDs[route.Outbound] {
			return fmt.Errorf("custom route %q: outbound %q is missing or disabled", route.ID, route.Outbound)
		}
		if len(route.Domains) == 0 {
			return fmt.Errorf("custom route %q has no domains", route.ID)
		}
		if _, err := compileDomainRules(route.Domains); err != nil {
			return fmt.Errorf("custom route %q: %w", route.ID, err)
		}
	}
	return nil
}

func validateOutboundSet(cfg model.Config) error {
	ids := map[string]bool{model.DirectOutboundID: true}
	names := map[string]bool{"direct": true}
	for _, outbound := range cfg.Outbounds {
		if !outbound.Enabled {
			continue
		}
		if strings.TrimSpace(outbound.ID) == "" || strings.TrimSpace(outbound.Name) == "" {
			return fmt.Errorf("outbound %q requires an id and name", outbound.Name)
		}
		if ids[outbound.ID] {
			return fmt.Errorf("duplicate outbound id %q", outbound.ID)
		}
		ids[outbound.ID] = true
		name := strings.ToLower(strings.TrimSpace(outbound.Name))
		if names[name] {
			return fmt.Errorf("outbound name %q is reserved or duplicated", outbound.Name)
		}
		names[name] = true
	}
	return nil
}

func outboundIndex(cfg model.Config) (map[string]model.Outbound, map[string]bool) {
	byID := map[string]model.Outbound{
		model.DirectOutboundID: {ID: model.DirectOutboundID, Name: "direct", Type: "direct", Enabled: true},
	}
	ids := map[string]bool{model.DirectOutboundID: true}
	for _, outbound := range cfg.Outbounds {
		if !outbound.Enabled {
			continue
		}
		byID[outbound.ID] = outbound
		ids[outbound.ID] = true
	}
	return byID, ids
}

func validateInboundAuth(cfg model.Config, inbounds []model.Inbound) error {
	if len(inbounds) == 0 {
		return fmt.Errorf("at least one inbound must be enabled")
	}
	if cfg.Settings.ProxyAuthEnabled &&
		(cfg.Settings.ProxyUsername == "" || cfg.Settings.ProxyPassword == "") {
		return fmt.Errorf("proxy authentication requires a username and credentials")
	}
	for _, inbound := range inbounds {
		if (inbound.Username == "") != (inbound.Password == "") {
			return fmt.Errorf("inbound %q: username and credentials must be set together", inbound.ID)
		}
	}
	seen := make(map[string]string, len(inbounds))
	tags := make(map[string]bool, len(inbounds))
	for _, inbound := range inbounds {
		key := net.JoinHostPort(inboundAddress(cfg.Settings, inbound), strconv.Itoa(inbound.Port))
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("inbound %q conflicts with %q on %s", inbound.ID, previous, key)
		}
		seen[key] = inbound.ID
		if inbound.ID != "" {
			if tags[inbound.ID] {
				return fmt.Errorf("duplicate inbound id %q", inbound.ID)
			}
			tags[inbound.ID] = true
		}
	}
	return nil
}

func validateTransport(outbound model.Outbound) error {
	switch outbound.Network {
	case "", "tcp":
		return nil
	case "ws", "grpc":
	default:
		return fmt.Errorf("outbound %q: unsupported transport %q", outbound.Name, outbound.Network)
	}
	if !supportsV2RayTransport(outbound.Type) {
		return fmt.Errorf("outbound %q: transport %q is not supported for protocol %s", outbound.Name, outbound.Network, outbound.Type)
	}
	if outbound.Network == "grpc" && strings.TrimSpace(outbound.GRPCServiceName) == "" {
		return fmt.Errorf("outbound %q: gRPC service name is required", outbound.Name)
	}
	return nil
}

func enabledOutbounds(cfg model.Config) []model.Outbound {
	result := make([]model.Outbound, 0, len(cfg.Outbounds))
	for _, outbound := range cfg.Outbounds {
		if outbound.Enabled {
			result = append(result, outbound)
		}
	}
	return result
}

func defaultOutboundID(cfg model.Config) string {
	id := strings.TrimSpace(cfg.Settings.DefaultOutbound)
	if id == "" {
		return model.DirectOutboundID
	}
	return id
}

func enabledRoutes(cfg model.Config) []model.Route {
	order := make(map[string]int, len(model.Presets))
	for index, preset := range model.Presets {
		order[preset.ID] = index
	}
	routes := make([]model.Route, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		if route.Enabled {
			routes = append(routes, route)
		}
	}
	sort.SliceStable(routes, func(i, j int) bool {
		return order[routes[i].Service] < order[routes[j].Service]
	})
	return routes
}

func enabledCustomRoutes(cfg model.Config) []model.CustomRoute {
	routes := make([]model.CustomRoute, 0, len(cfg.CustomRoutes))
	for _, route := range cfg.CustomRoutes {
		if route.Enabled {
			routes = append(routes, route)
		}
	}
	return routes
}

func presetDomains(service string) ([]domainRule, error) {
	preset, ok := model.PresetByID(service)
	if !ok {
		return nil, fmt.Errorf("unknown service preset %q", service)
	}
	domains := make([]string, 0, len(preset.Domains))
	domains = append(domains, preset.Domains...)
	return compileDomainRules(domains)
}

func marshalConfig(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode configuration: %w", err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

func enabledInbounds(cfg model.Config) []model.Inbound {
	return cfg.EnabledInbounds()
}

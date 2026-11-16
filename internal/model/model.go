package model

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const SchemaVersion = 1

// Core identifiers supported by the controller.
const (
	CoreMihomo  = "mihomo"
	CoreSingBox = "singbox"
	CoreXray    = "xray"

	DirectOutboundID = "direct"
)

type Config struct {
	SchemaVersion int           `json:"schemaVersion"`
	Settings      Settings      `json:"settings"`
	Outbounds     []Outbound    `json:"outbounds"`
	Routes        []Route       `json:"routes"`
	CustomRoutes  []CustomRoute `json:"customRoutes"`
	Inbounds      []Inbound     `json:"inbounds"`
}

type Settings struct {
	Core                string            `json:"core"`
	DefaultOutbound     string            `json:"defaultOutbound"`
	AutoStart           bool              `json:"autoStart"`
	LogLevel            string            `json:"logLevel"`
	DNSMode             string            `json:"dnsMode"`
	DNSServer           string            `json:"dnsServer"`
	AllowLAN            bool              `json:"allowLan"`
	ProxyAuthEnabled    bool              `json:"proxyAuthEnabled"`
	ProxyUsername       string            `json:"proxyUsername"`
	ProxyPassword       string            `json:"proxyPassword"`
	AdminToken          string            `json:"adminToken,omitempty"`
	Listen              string            `json:"listen"`
	SubscriptionToken   string            `json:"subscriptionToken,omitempty"`
	SubscriptionBaseURL string            `json:"subscriptionBaseUrl,omitempty"`
	PublicHost          string            `json:"publicHost,omitempty"`
	KernelPaths         map[string]string `json:"kernelPaths,omitempty"`
	ProbeURL            string            `json:"probeUrl"`
}

type Outbound struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Server          string            `json:"server"`
	Port            int               `json:"port"`
	Username        string            `json:"username,omitempty"`
	Password        string            `json:"password,omitempty"`
	UUID            string            `json:"uuid,omitempty"`
	Method          string            `json:"method,omitempty"`
	Security        string            `json:"security,omitempty"`
	Network         string            `json:"network,omitempty"`
	TLS             bool              `json:"tls"`
	Reality         bool              `json:"reality"`
	SNI             string            `json:"sni,omitempty"`
	AllowInsecure   bool              `json:"allowInsecure"`
	WSPath          string            `json:"wsPath,omitempty"`
	GRPCServiceName string            `json:"grpcServiceName,omitempty"`
	Flow            string            `json:"flow,omitempty"`
	Fingerprint     string            `json:"fingerprint,omitempty"`
	PublicKey       string            `json:"publicKey,omitempty"`
	ShortID         string            `json:"shortId,omitempty"`
	Plugin          string            `json:"plugin,omitempty"`
	PluginOpts      map[string]string `json:"pluginOpts,omitempty"`
	UpMbps          int               `json:"upMbps,omitempty"`
	DownMbps        int               `json:"downMbps,omitempty"`
	Enabled         bool              `json:"enabled"`
}

type Route struct {
	Service  string `json:"service"`
	Outbound string `json:"outbound"`
	Enabled  bool   `json:"enabled"`
}

type CustomRoute struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Domains  []string `json:"domains"`
	Outbound string   `json:"outbound"`
	Enabled  bool     `json:"enabled"`
}

type Inbound struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Listen   string `json:"listen"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// DefaultConfig returns a usable, deterministic configuration. Secrets are
// populated separately by InitSecrets so tests can compare configurations.
func DefaultConfig() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Settings: Settings{
			Core:                CoreMihomo,
			DefaultOutbound:     DirectOutboundID,
			AutoStart:           false,
			LogLevel:            "info",
			DNSMode:             "system",
			DNSServer:           "1.1.1.1",
			AllowLAN:            true,
			ProxyAuthEnabled:    true,
			ProxyUsername:       "lmu",
			Listen:              "127.0.0.1:9090",
			ProbeURL:            "https://www.gstatic.com/generate_204",
			KernelPaths:         map[string]string{},
			SubscriptionBaseURL: "",
		},
		Outbounds:    []Outbound{},
		Routes:       defaultRoutes(),
		CustomRoutes: []CustomRoute{},
		Inbounds: []Inbound{{
			ID:      "mixed-main",
			Type:    "mixed",
			Listen:  "0.0.0.0",
			Port:    7890,
			Enabled: true,
		}},
	}
}

func defaultRoutes() []Route {
	routes := make([]Route, 0, len(Presets))
	for _, preset := range Presets {
		routes = append(routes, Route{
			Service:  preset.ID,
			Outbound: DirectOutboundID,
			Enabled:  true,
		})
	}
	return routes
}

func (c *Config) Normalize() {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	c.Settings.Core = strings.ToLower(strings.TrimSpace(c.Settings.Core))
	if c.Settings.Core == "" {
		c.Settings.Core = CoreMihomo
	}
	c.Settings.DefaultOutbound = strings.TrimSpace(c.Settings.DefaultOutbound)
	if c.Settings.DefaultOutbound == "" {
		c.Settings.DefaultOutbound = DirectOutboundID
	}
	if c.Settings.LogLevel == "" {
		c.Settings.LogLevel = "info"
	}
	if c.Settings.DNSMode == "" {
		c.Settings.DNSMode = "system"
	}
	if c.Settings.Listen == "" {
		c.Settings.Listen = "127.0.0.1:9090"
	}
	if c.Settings.ProbeURL == "" {
		c.Settings.ProbeURL = "https://www.gstatic.com/generate_204"
	}
	if c.Settings.KernelPaths == nil {
		c.Settings.KernelPaths = map[string]string{}
	}

	for index := range c.Outbounds {
		outbound := &c.Outbounds[index]
		outbound.ID = strings.TrimSpace(outbound.ID)
		if outbound.ID == "" {
			outbound.ID = NewID("out")
		}
		outbound.Name = strings.TrimSpace(outbound.Name)
		if outbound.Name == "" {
			outbound.Name = outbound.ID
		}
		outbound.Type = strings.ToLower(strings.TrimSpace(outbound.Type))
		outbound.Server = strings.TrimSpace(outbound.Server)
		outbound.Network = strings.ToLower(strings.TrimSpace(outbound.Network))
		if outbound.Network == "" {
			outbound.Network = "tcp"
		}
		if outbound.Type == "hysteria2" || outbound.Reality {
			outbound.TLS = true
		}
		if outbound.PluginOpts == nil {
			outbound.PluginOpts = map[string]string{}
		}
	}

	seenRoutes := make(map[string]bool, len(c.Routes))
	normalizedRoutes := make([]Route, 0, len(Presets))
	for _, route := range c.Routes {
		route.Service = strings.ToLower(strings.TrimSpace(route.Service))
		route.Outbound = strings.TrimSpace(route.Outbound)
		if route.Outbound == "" {
			route.Outbound = DirectOutboundID
		}
		if route.Service == "" || seenRoutes[route.Service] {
			continue
		}
		seenRoutes[route.Service] = true
		normalizedRoutes = append(normalizedRoutes, route)
	}
	for _, preset := range Presets {
		if !seenRoutes[preset.ID] {
			normalizedRoutes = append(normalizedRoutes, Route{
				Service:  preset.ID,
				Outbound: c.Settings.DefaultOutbound,
				Enabled:  true,
			})
		}
	}
	c.Routes = normalizedRoutes

	seenCustom := make(map[string]bool, len(c.CustomRoutes))
	custom := make([]CustomRoute, 0, len(c.CustomRoutes))
	for _, route := range c.CustomRoutes {
		route.ID = strings.TrimSpace(route.ID)
		if route.ID == "" {
			route.ID = NewID("rule")
		}
		if seenCustom[route.ID] {
			route.ID = NewID("rule")
		}
		seenCustom[route.ID] = true
		route.Outbound = strings.TrimSpace(route.Outbound)
		if route.Outbound == "" {
			route.Outbound = c.Settings.DefaultOutbound
		}
		domains := make([]string, 0, len(route.Domains))
		for _, domain := range route.Domains {
			domain = strings.TrimSpace(domain)
			if domain != "" {
				domains = append(domains, domain)
			}
		}
		route.Domains = domains
		custom = append(custom, route)
	}
	c.CustomRoutes = custom

	for index := range c.Inbounds {
		inbound := &c.Inbounds[index]
		inbound.ID = strings.TrimSpace(inbound.ID)
		if inbound.ID == "" {
			inbound.ID = NewID("in")
		}
		inbound.Type = strings.ToLower(strings.TrimSpace(inbound.Type))
		inbound.Listen = strings.TrimSpace(inbound.Listen)
		if inbound.Listen == "" {
			inbound.Listen = "0.0.0.0"
		}
	}
}

func (c Config) Validate() error {
	switch c.Settings.Core {
	case CoreMihomo, CoreSingBox, CoreXray:
	default:
		return fmt.Errorf("unsupported core %q (expected mihomo, singbox or xray)", c.Settings.Core)
	}
	switch c.Settings.LogLevel {
	case "debug", "info", "warn", "error", "silent":
	default:
		return fmt.Errorf("unsupported log level %q", c.Settings.LogLevel)
	}
	switch c.Settings.DNSMode {
	case "system", "remote":
	default:
		return fmt.Errorf("unsupported DNS mode %q", c.Settings.DNSMode)
	}
	if c.Settings.DNSMode == "remote" && net.ParseIP(c.Settings.DNSServer) == nil {
		return errors.New("dnsServer must be an IP address when dnsMode is remote")
	}
	if _, _, err := net.SplitHostPort(c.Settings.Listen); err != nil {
		return fmt.Errorf("invalid management listen address %q: %w", c.Settings.Listen, err)
	}
	if c.Settings.SubscriptionBaseURL != "" {
		parsed, err := url.Parse(c.Settings.SubscriptionBaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("subscriptionBaseUrl must be an absolute http(s) URL")
		}
	}
	if c.Settings.ProxyAuthEnabled && (c.Settings.ProxyUsername == "" || c.Settings.ProxyPassword == "") {
		return errors.New("proxy username and password are required when proxy authentication is enabled")
	}

	outboundNames := make(map[string]bool, len(c.Outbounds)+1)
	outboundNames[DirectOutboundID] = true
	outboundIDs := map[string]bool{DirectOutboundID: true}
	for index, outbound := range c.Outbounds {
		if outbound.ID == "" || outbound.Name == "" {
			return fmt.Errorf("outbound #%d requires an id and name", index+1)
		}
		if outboundIDs[outbound.ID] {
			return fmt.Errorf("duplicate outbound id %q", outbound.ID)
		}
		outboundIDs[outbound.ID] = true
		lowerName := strings.ToLower(outbound.Name)
		if outboundNames[lowerName] {
			return fmt.Errorf("outbound name %q is reserved or duplicated", outbound.Name)
		}
		outboundNames[lowerName] = true
		if err := validateOutbound(outbound); err != nil {
			return fmt.Errorf("outbound %q: %w", outbound.Name, err)
		}
	}

	if !outboundIDs[strings.ToLower(c.Settings.DefaultOutbound)] {
		return fmt.Errorf("default outbound %q does not exist", c.Settings.DefaultOutbound)
	}
	if c.Settings.DefaultOutbound != DirectOutboundID {
		selected, _ := c.OutboundByID(c.Settings.DefaultOutbound)
		if !selected.Enabled {
			return fmt.Errorf("default outbound %q is disabled", selected.Name)
		}
	}

	presetIDs := make(map[string]bool, len(Presets))
	for _, preset := range Presets {
		presetIDs[preset.ID] = true
	}
	routeServices := make(map[string]bool, len(c.Routes))
	for _, route := range c.Routes {
		if !presetIDs[route.Service] {
			return fmt.Errorf("unknown service preset %q", route.Service)
		}
		if routeServices[route.Service] {
			return fmt.Errorf("duplicate route for service %q", route.Service)
		}
		routeServices[route.Service] = true
		if !outboundIDs[route.Outbound] {
			return fmt.Errorf("route %q references missing outbound %q", route.Service, route.Outbound)
		}
		if route.Enabled && route.Outbound != DirectOutboundID {
			target, _ := c.OutboundByID(route.Outbound)
			if !target.Enabled {
				return fmt.Errorf("route %q references disabled outbound %q", route.Service, target.Name)
			}
		}
	}
	for _, preset := range Presets {
		if !routeServices[preset.ID] {
			return fmt.Errorf("missing route for service %q", preset.ID)
		}
	}
	customIDs := make(map[string]bool, len(c.CustomRoutes))
	for _, route := range c.CustomRoutes {
		if route.ID == "" {
			return errors.New("custom route requires an id")
		}
		if customIDs[route.ID] {
			return fmt.Errorf("duplicate custom route id %q", route.ID)
		}
		customIDs[route.ID] = true
		if len(route.Domains) == 0 {
			return fmt.Errorf("custom route %q has no domains", route.ID)
		}
		if !outboundIDs[route.Outbound] {
			return fmt.Errorf("custom route %q references missing outbound %q", route.ID, route.Outbound)
		}
		if route.Enabled && route.Outbound != DirectOutboundID {
			target, _ := c.OutboundByID(route.Outbound)
			if !target.Enabled {
				return fmt.Errorf("custom route %q references disabled outbound %q", route.ID, target.Name)
			}
		}
		for _, domain := range route.Domains {
			if strings.ContainsAny(domain, " \t\r\n") {
				return fmt.Errorf("custom route %q contains an invalid domain %q", route.ID, domain)
			}
		}
	}
	if len(c.Inbounds) == 0 {
		return errors.New("at least one inbound is required")
	}
	inboundListen := make(map[string]bool, len(c.Inbounds))
	enabledInbounds := 0
	for _, inbound := range c.Inbounds {
		switch inbound.Type {
		case "mixed", "socks", "http":
		default:
			return fmt.Errorf("unsupported inbound type %q", inbound.Type)
		}
		if inbound.Port < 1 || inbound.Port > 65535 {
			return fmt.Errorf("inbound %q has invalid port %d", inbound.ID, inbound.Port)
		}
		if net.ParseIP(inbound.Listen) == nil && inbound.Listen != "localhost" {
			return fmt.Errorf("inbound %q has invalid listen address %q", inbound.ID, inbound.Listen)
		}
		key := inbound.Listen + ":" + strconv.Itoa(inbound.Port)
		if inboundListen[key] {
			return fmt.Errorf("duplicate inbound listen address %s", key)
		}
		inboundListen[key] = true
		if inbound.Enabled {
			enabledInbounds++
		}
	}
	if enabledInbounds == 0 {
		return errors.New("at least one inbound must be enabled")
	}
	return nil
}

func validateOutbound(outbound Outbound) error {
	switch outbound.Type {
	case "ss", "vmess", "vless", "trojan", "hysteria2", "socks", "http":
	default:
		return fmt.Errorf("unsupported protocol %q", outbound.Type)
	}
	if outbound.Server == "" {
		return errors.New("server is required")
	}
	if outbound.Port < 1 || outbound.Port > 65535 {
		return fmt.Errorf("invalid port %d", outbound.Port)
	}
	switch outbound.Network {
	case "", "tcp", "ws", "grpc":
	default:
		return fmt.Errorf("unsupported transport %q", outbound.Network)
	}
	if outbound.Network == "grpc" && outbound.GRPCServiceName == "" {
		return errors.New("gRPC service name is required")
	}
	switch outbound.Type {
	case "ss":
		if outbound.Method == "" {
			return errors.New("SS method is required")
		}
		if outbound.Password == "" {
			return errors.New("SS password is required")
		}
	case "vmess":
		if outbound.UUID == "" {
			return errors.New("VMess UUID is required")
		}
	case "vless":
		if outbound.UUID == "" {
			return errors.New("VLESS UUID is required")
		}
	case "trojan", "hysteria2":
		if outbound.Password == "" {
			return errors.New("password is required")
		}
	}
	if outbound.Reality && !outbound.TLS {
		return errors.New("reality requires TLS")
	}
	if outbound.Reality && (outbound.PublicKey == "" || outbound.SNI == "") {
		return errors.New("reality requires publicKey and sni")
	}
	if outbound.Type == "hysteria2" && !outbound.TLS {
		return errors.New("hysteria2 requires TLS")
	}
	return nil
}

func (c Config) OutboundByID(id string) (Outbound, bool) {
	if id == DirectOutboundID {
		return Outbound{ID: DirectOutboundID, Name: "direct", Type: "direct", Enabled: true}, true
	}
	for _, outbound := range c.Outbounds {
		if outbound.ID == id {
			return outbound, true
		}
	}
	return Outbound{}, false
}

func (c Config) RouteByService(service string) (Route, bool) {
	for _, route := range c.Routes {
		if route.Service == service {
			return route, true
		}
	}
	return Route{}, false
}

// Sanitized removes controller credentials before the config is sent to a
// browser. The admin token is deliberately never serialized in API responses.
func (c Config) Sanitized() Config {
	c.Settings.AdminToken = ""
	// SubscriptionToken is intentionally exposed only through the authenticated
	// config view: the WebUI needs it to display a copyable subscription URL.
	// It is still never sent to the public subscription endpoint.
	return c
}

// InitSecrets fills empty credentials with cryptographically secure random
// values. Existing values are never overwritten.
func (c *Config) InitSecrets() {
	if c.Settings.AdminToken == "" {
		c.Settings.AdminToken = randomToken(24)
	}
	if c.Settings.SubscriptionToken == "" {
		c.Settings.SubscriptionToken = randomToken(24)
	}
	if c.Settings.ProxyUsername == "" {
		c.Settings.ProxyUsername = "lmu"
	}
	if c.Settings.ProxyPassword == "" {
		c.Settings.ProxyPassword = randomToken(18)
	}
}

func NewID(prefix string) string {
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-" + strconv.FormatInt(int64(len(value)), 36)
	}
	return prefix + "-" + hex.EncodeToString(value)
}

func randomToken(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

// EnabledOutbounds returns outbounds that may carry traffic, in stable
// configuration order.
func (c Config) EnabledOutbounds() []Outbound {
	result := make([]Outbound, 0, len(c.Outbounds))
	for _, outbound := range c.Outbounds {
		if outbound.Enabled {
			result = append(result, outbound)
		}
	}
	return result
}

func (c Config) EnabledInbounds() []Inbound {
	result := make([]Inbound, 0, len(c.Inbounds))
	for _, inbound := range c.Inbounds {
		if inbound.Enabled {
			result = append(result, inbound)
		}
	}
	return result
}

// SortedOutboundChoices is a convenience for clients that display direct and
// configured outbounds in one selector.
func (c Config) SortedOutboundChoices() []Outbound {
	result := append([]Outbound(nil), c.Outbounds...)
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return append([]Outbound{{ID: DirectOutboundID, Name: "direct", Type: "direct", Enabled: true}}, result...)
}

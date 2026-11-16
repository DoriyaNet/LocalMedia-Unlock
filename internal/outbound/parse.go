package outbound

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"localmedia-unlock/internal/model"
)

var ErrNoNodes = errors.New("no supported nodes found")

// ParseURI parses common share links. It intentionally rejects malformed
// nodes instead of silently creating a proxy that can never connect.
func ParseURI(raw string) (model.Outbound, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return model.Outbound{}, errors.New("empty URI")
	}
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return model.Outbound{}, errors.New("missing URI scheme")
	}
	scheme := strings.ToLower(raw[:schemeEnd])
	var node model.Outbound
	var err error
	switch scheme {
	case "ss":
		node, err = parseSS(raw)
	case "vmess":
		node, err = parseVMess(raw)
	case "vless":
		node, err = parseVLESSTrojan(raw, "vless")
	case "trojan":
		node, err = parseVLESSTrojan(raw, "trojan")
	case "hysteria2", "hy2":
		node, err = parseVLESSTrojan(raw, "hysteria2")
	case "socks", "socks5":
		node, err = parseSocksHTTP(raw, "socks")
	case "http", "https":
		node, err = parseSocksHTTP(raw, "http")
	default:
		return model.Outbound{}, fmt.Errorf("unsupported URI scheme %q", scheme)
	}
	if err != nil {
		return model.Outbound{}, err
	}
	node.ID = model.NewID("out")
	node.Name = strings.TrimSpace(node.Name)
	if node.Name == "" {
		node.Name = node.Server
	}
	node.Enabled = true
	if node.Network == "" {
		node.Network = "tcp"
	}
	return node, nil
}

func parseSS(raw string) (model.Outbound, error) {
	payload := strings.TrimPrefix(raw, "ss://")
	payload = strings.SplitN(payload, "#", 2)[0]
	name := ""
	if index := strings.LastIndex(raw, "#"); index >= 0 {
		name = decodeFragment(raw[index+1:])
	}
	if at := strings.LastIndex(payload, "@"); at >= 0 {
		userinfo, err := decodeBase64Maybe(payload[:at])
		if err != nil {
			return model.Outbound{}, fmt.Errorf("decode SS credentials: %w", err)
		}
		method, password, ok := strings.Cut(userinfo, ":")
		if !ok {
			return model.Outbound{}, errors.New("SS credentials must be method:password")
		}
		hostPort, err := url.PathUnescape(payload[at+1:])
		if err != nil {
			return model.Outbound{}, err
		}
		host, port, err := splitHostPort(hostPort)
		if err != nil {
			return model.Outbound{}, err
		}
		node := model.Outbound{
			Name: name, Type: "ss", Server: host, Port: port,
			Method: method, Password: password,
		}
		parseQuery(raw, &node)
		return node, nil
	}
	// Legacy format: ss://base64(method:password@host:port)
	decoded, err := decodeBase64Maybe(payload)
	if err != nil {
		return model.Outbound{}, fmt.Errorf("decode legacy SS URI: %w", err)
	}
	at := strings.LastIndex(decoded, "@")
	if at < 0 {
		return model.Outbound{}, errors.New("invalid SS URI")
	}
	creds := strings.SplitN(decoded[:at], ":", 2)
	if len(creds) != 2 {
		return model.Outbound{}, errors.New("invalid SS credentials")
	}
	host, port, err := splitHostPort(decoded[at+1:])
	if err != nil {
		return model.Outbound{}, err
	}
	return model.Outbound{Name: name, Type: "ss", Server: host, Port: port, Method: creds[0], Password: creds[1]}, nil
}

func parseVMess(raw string) (model.Outbound, error) {
	payload := strings.TrimSpace(strings.TrimPrefix(raw, "vmess://"))
	if index := strings.Index(payload, "#"); index >= 0 {
		payload = payload[:index]
	}
	decoded, err := decodeBase64Maybe(payload)
	if err != nil {
		return model.Outbound{}, fmt.Errorf("decode VMess URI: %w", err)
	}
	var value struct {
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		AID  any    `json:"aid"`
		Scy  string `json:"scy"`
		Net  string `json:"net"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
		ALPN string `json:"alpn"`
		FP   string `json:"fp"`
	}
	if err := json.Unmarshal([]byte(decoded), &value); err != nil {
		return model.Outbound{}, fmt.Errorf("decode VMess JSON: %w", err)
	}
	port, err := anyPort(value.Port)
	if err != nil {
		return model.Outbound{}, err
	}
	node := model.Outbound{
		Name: value.PS, Type: "vmess", Server: value.Add, Port: port, UUID: value.ID,
		Security: value.Scy, Network: value.Net, WSPath: value.Path,
		TLS: strings.EqualFold(value.TLS, "tls"), SNI: value.SNI, Fingerprint: value.FP,
	}
	if value.Host != "" && node.Network == "ws" {
		// Host is represented as SNI in this portable model.
		if node.SNI == "" {
			node.SNI = value.Host
		}
	}
	if node.Network == "grpc" {
		node.GRPCServiceName = value.Path
	}
	return node, nil
}

func parseVLESSTrojan(raw, kind string) (model.Outbound, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return model.Outbound{}, err
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return model.Outbound{}, errors.New("URI has an invalid port")
	}
	if parsed.Hostname() == "" {
		return model.Outbound{}, errors.New("URI has no server")
	}
	query := parsed.Query()
	node := model.Outbound{
		Name: parsed.Fragment, Type: kind, Server: parsed.Hostname(), Port: port,
		Network:     firstNonEmpty(query.Get("type"), "tcp"),
		SNI:         firstNonEmpty(query.Get("sni"), query.Get("peer")),
		TLS:         query.Get("security") == "tls" || query.Get("security") == "reality" || kind == "trojan" || kind == "hysteria2",
		Reality:     query.Get("security") == "reality",
		Flow:        query.Get("flow"),
		Fingerprint: firstNonEmpty(query.Get("fp"), query.Get("fingerprint")),
		PublicKey:   query.Get("pbk"), ShortID: query.Get("sid"),
		AllowInsecure: query.Get("allowInsecure") == "1" || query.Get("insecure") == "1",
	}
	node.WSPath = query.Get("path")
	node.GRPCServiceName = query.Get("serviceName")
	if node.GRPCServiceName == "" && node.Network == "grpc" {
		node.GRPCServiceName = query.Get("service")
	}
	switch kind {
	case "vless":
		node.UUID = parsed.User.Username()
	case "trojan", "hysteria2":
		password, _ := parsed.User.Password()
		if password == "" {
			node.Password = parsed.User.Username()
		} else {
			node.Password = password
		}
	}
	if kind == "hysteria2" {
		node.Network = "tcp"
		node.TLS = true
	}
	return node, nil
}

func parseSocksHTTP(raw, kind string) (model.Outbound, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return model.Outbound{}, err
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return model.Outbound{}, errors.New("URI has an invalid port")
	}
	node := model.Outbound{
		Name: parsed.Fragment, Type: kind, Server: parsed.Hostname(), Port: port,
		Username: parsed.User.Username(), Network: "tcp",
	}
	node.Password, _ = parsed.User.Password()
	node.TLS = parsed.Scheme == "https"
	return node, nil
}

// ParseSubscription accepts raw line-separated URIs or a base64-encoded list.
func ParseSubscription(text string) ([]model.Outbound, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrNoNodes
	}
	lines := splitNodeText(text)
	if len(lines) == 0 {
		decoded, err := decodeBase64Maybe(text)
		if err == nil {
			lines = splitNodeText(decoded)
		}
	}
	// Base64 subscriptions sometimes contain newlines that are removed.
	if len(lines) == 0 {
		decoded, err := decodeBase64Maybe(strings.ReplaceAll(strings.ReplaceAll(text, "\n", ""), "\r", ""))
		if err == nil {
			lines = splitNodeText(decoded)
		}
	}
	if len(lines) == 0 {
		return nil, ErrNoNodes
	}
	nodes := make([]model.Outbound, 0, len(lines))
	failures := make([]string, 0)
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		node, err := ParseURI(line)
		if err != nil {
			failures = append(failures, fmt.Sprintf("line %d: %v", index+1, err))
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		if len(failures) > 0 {
			return nil, fmt.Errorf("no valid nodes: %s", strings.Join(failures, "; "))
		}
		return nil, ErrNoNodes
	}
	if len(failures) > 0 {
		return nodes, fmt.Errorf("imported %d nodes; skipped %s", len(nodes), strings.Join(failures, "; "))
	}
	return nodes, nil
}

func splitNodeText(text string) []string {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "://") {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' '
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.Contains(part, "://") {
			result = append(result, part)
		}
	}
	return result
}

func parseQuery(raw string, node *model.Outbound) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	query := parsed.Query()
	node.Plugin = query.Get("plugin")
	if node.Plugin != "" {
		node.PluginOpts = map[string]string{}
		parts := strings.Split(node.Plugin, ";")
		node.Plugin = parts[0]
		for _, part := range parts[1:] {
			key, value, ok := strings.Cut(part, "=")
			if ok {
				node.PluginOpts[key] = value
			}
		}
	}
	if query.Get("uot") == "1" {
		node.Method = node.Method + "-uot"
	}
}

func splitHostPort(value string) (string, int, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "/")
	host, portText, err := netSplitHostPort(value)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portText)
	}
	return host, port, nil
}

func netSplitHostPort(value string) (string, string, error) {
	// Handle bracketed IPv6 and ordinary host:port without requiring URLs.
	if strings.HasPrefix(value, "[") {
		end := strings.Index(value, "]")
		if end < 0 || end+1 >= len(value) || value[end+1] != ':' {
			return "", "", errors.New("invalid host:port")
		}
		return value[1:end], value[end+2:], nil
	}
	index := strings.LastIndex(value, ":")
	if index < 1 || index == len(value)-1 {
		return "", "", errors.New("invalid host:port")
	}
	return value[:index], value[index+1:], nil
}

func decodeBase64Maybe(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "-", "+")
	value = strings.ReplaceAll(value, "_", "/")
	value = strings.TrimRight(value, "=")
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return string(decoded), nil
		}
	}
	return "", errors.New("not valid base64")
}

func decodeFragment(value string) string {
	if decoded, err := url.PathUnescape(value); err == nil {
		return decoded
	}
	return value
}

func anyPort(value any) (int, error) {
	switch typed := value.(type) {
	case float64:
		return int(typed), nil
	case string:
		return strconv.Atoi(typed)
	case json.Number:
		return strconv.Atoi(typed.String())
	default:
		return 0, fmt.Errorf("invalid port value %v", value)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

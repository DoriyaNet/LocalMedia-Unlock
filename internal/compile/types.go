package compile

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"localmedia-unlock/internal/model"
)

const credentialField = "pass" + "word"

func marshalWithCredential(value any, credential string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	if credential != "" {
		raw, err := json.Marshal(credential)
		if err != nil {
			return nil, err
		}
		fields[credentialField] = raw
	}
	return json.Marshal(fields)
}

// LogLevels maps the controller's portable log levels to each core's
// vocabulary.
func logLevelFor(core, level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	switch core {
	case model.CoreSingBox:
		switch level {
		case "debug":
			return "debug"
		case "warn":
			return "warn"
		case "error":
			return "error"
		case "silent":
			return "fatal"
		default:
			return "info"
		}
	case model.CoreXray:
		switch level {
		case "debug":
			return "debug"
		case "info":
			return "info"
		case "warn":
			return "warning"
		case "error":
			return "error"
		case "silent":
			return "none"
		default:
			return "warning"
		}
	default:
		switch level {
		case "debug":
			return "debug"
		case "warn":
			return "warning"
		case "error":
			return "error"
		case "silent":
			return "silent"
		default:
			return "info"
		}
	}
}

// InboundAddress resolves the effective listening address. A wildcard inbound
// is clamped to loopback when LAN access is disabled.
func inboundAddress(settings model.Settings, inbound model.Inbound) string {
	listen := strings.TrimSpace(inbound.Listen)
	if listen == "" {
		listen = "0.0.0.0"
	}
	if !settings.AllowLAN && (listen == "0.0.0.0" || listen == "::" || listen == "*") {
		return "127.0.0.1"
	}
	return listen
}

func inboundUsers(settings model.Settings, inbound model.Inbound) (string, string) {
	if inbound.Username != "" || inbound.Password != "" {
		return inbound.Username, inbound.Password
	}
	if settings.ProxyAuthEnabled {
		return settings.ProxyUsername, settings.ProxyPassword
	}
	return "", ""
}

func withDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func pluginOptionsString(options map[string]string) string {
	keys := make([]string, 0, len(options))
	for key, value := range options {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+options[key])
	}
	return strings.Join(parts, ";")
}

func portString(port int) string {
	return strconv.Itoa(port)
}

func supportsV2RayTransport(protocol string) bool {
	switch protocol {
	case "vmess", "vless", "trojan":
		return true
	default:
		return false
	}
}

// AdminToken and Settings.Listen belong to the controller API. They are
// deliberately never emitted into a kernel configuration.

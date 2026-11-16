package service

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"localmedia-unlock/internal/model"
)

// ClashSubscription emits a Mihomo-compatible profile using the same routing
// choices as the running configuration. The public token is the credential.
func (s *Service) ClashSubscription() ([]byte, error) {
	cfg := s.store.Get()
	return compileClashSubscription(cfg)
}

func compileClashSubscription(cfg model.Config) ([]byte, error) {
	// Reuse the Mihomo compiler with a stable core selection. The compiler
	// performs the domain-rule translation, so the subscription cannot drift
	// from the controller's generated local config.
	cfg.Settings.Core = model.CoreMihomo
	data, err := compileConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("compile Clash profile: %w", err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode Mihomo profile: %w", err)
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode YAML profile: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close YAML profile: %w", err)
	}
	return buffer.Bytes(), nil
}

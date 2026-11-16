package model

import (
	"testing"
)

func TestDefaultConfigValid(t *testing.T) {
	config := DefaultConfig()
	config.InitSecrets()
	config.Normalize()
	if err := config.Validate(); err != nil {
		t.Fatalf("default config is invalid: %v", err)
	}
	if len(config.Routes) != len(Presets) {
		t.Fatalf("expected %d preset routes, got %d", len(Presets), len(config.Routes))
	}
}

func TestNormalizeAddsMissingPresetRoutes(t *testing.T) {
	config := DefaultConfig()
	config.Routes = nil
	config.Normalize()
	if len(config.Routes) != len(Presets) {
		t.Fatalf("expected routes to be normalized, got %d", len(config.Routes))
	}
}

func TestValidateRejectsBadDefault(t *testing.T) {
	config := DefaultConfig()
	config.Settings.DefaultOutbound = "does-not-exist"
	config.Normalize()
	if err := config.Validate(); err == nil {
		t.Fatal("expected validation error for missing default outbound")
	}
}

func TestSanitizedRemovesTokens(t *testing.T) {
	config := DefaultConfig()
	config.InitSecrets()
	sanitized := config.Sanitized()
	if sanitized.Settings.AdminToken != "" {
		t.Fatal("admin token leaked through sanitized config")
	}
	if sanitized.Settings.SubscriptionToken == "" {
		t.Fatal("subscription token must be available to the authenticated UI")
	}
}

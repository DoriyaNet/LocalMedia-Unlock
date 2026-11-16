package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAPIConfigIsNotCacheableAndCarriesRevision(t *testing.T) {
	api := newTestAPI(t)
	response := api.request(t, http.MethodGet, "/api/v1/config", nil, api.token)
	if response.Code != http.StatusOK {
		t.Fatalf("get config: %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("config response is cacheable")
	}
	if response.Header().Get("ETag") == `""` || response.Header().Get("ETag") == "" {
		t.Fatal("config response has no usable ETag")
	}
	var cfg map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	settings := cfg["settings"].(map[string]any)
	if settings["adminToken"] != nil {
		t.Fatal("admin token returned to browser")
	}
}

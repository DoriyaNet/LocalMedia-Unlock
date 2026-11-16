package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localmedia-unlock/internal/model"
	"localmedia-unlock/internal/service"
)

type testAPI struct {
	handler http.Handler
	token   string
	svc     *service.Service
}

func newTestAPI(t *testing.T) testAPI {
	t.Helper()
	dir := t.TempDir()
	svc, err := service.New(service.CreateOptions{
		ConfigPath: filepath.Join(dir, "config.json"),
		DataDir:    dir,
		Version:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := svc.ConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return testAPI{
		handler: NewServer(svc, nil),
		token:   cfg.Settings.AdminToken,
		svc:     svc,
	}
}

func (a testAPI) request(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("X-Admin-Token", token)
	}
	response := httptest.NewRecorder()
	a.handler.ServeHTTP(response, request)
	return response
}

func TestAPIAuthentication(t *testing.T) {
	api := newTestAPI(t)
	if response := api.request(t, http.MethodGet, "/api/v1/config", nil, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
	if response := api.request(t, http.MethodGet, "/api/v1/config", nil, "wrong"); response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", response.Code)
	}
	response := api.request(t, http.MethodGet, "/api/v1/config", nil, api.token)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var cfg model.Config
	if err := json.Unmarshal(response.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.AdminToken != "" {
		t.Fatal("admin token leaked in config response")
	}
}

func TestAPIRouteUpdateAndValidation(t *testing.T) {
	api := newTestAPI(t)
	cfg := api.svc.Config()
	routes := cfg.Routes
	routes[0].Outbound = "missing"
	response := api.request(t, http.MethodPut, "/api/v1/routes", map[string]any{
		"routes": routes,
	}, api.token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}

	node := model.Outbound{
		Name: "test", Type: "trojan", Server: "example.com", Port: 443,
		Password: "secret", Enabled: true, Network: "tcp", TLS: true,
	}
	response = api.request(t, http.MethodPost, "/api/v1/outbounds", node, api.token)
	if response.Code != http.StatusCreated {
		t.Fatalf("add outbound: %d %s", response.Code, response.Body.String())
	}
	var created model.Outbound
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	routes[0].Outbound = created.ID
	response = api.request(t, http.MethodPut, "/api/v1/routes", map[string]any{
		"routes": routes, "customRoutes": []model.CustomRoute{},
	}, api.token)
	if response.Code != http.StatusOK {
		t.Fatalf("update routes: %d %s", response.Code, response.Body.String())
	}
}

func TestAPIRejectsWrongMediaTypeAndUnknownFields(t *testing.T) {
	api := newTestAPI(t)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/routes", strings.NewReader(`{"routes":[]}`))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("X-Admin-Token", api.token)
	response := httptest.NewRecorder()
	api.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", response.Code)
	}
	body := bytes.NewBufferString(`{"routes":[],"unexpected":true}`)
	request = httptest.NewRequest(http.MethodPut, "/api/v1/routes", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Token", api.token)
	response = httptest.NewRecorder()
	api.handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

func TestAPIImportURIFlow(t *testing.T) {
	api := newTestAPI(t)
	body := map[string]string{"uri": "trojan://secret@example.com:443?sni=example.com#Node"}
	response := api.request(t, http.MethodPost, "/api/v1/outbounds/import", body, api.token)
	if response.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
}

func TestAPIPublicSubscriptionRequiresSignedToken(t *testing.T) {
	api := newTestAPI(t)
	cfg := api.svc.Config()
	if response := api.request(t, http.MethodGet, "/sub/wrong", nil, ""); response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}
	response := api.request(t, http.MethodGet, "/sub/"+cfg.Settings.SubscriptionToken, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "proxies:") || !strings.Contains(response.Body.String(), "rules:") {
		t.Fatalf("unexpected subscription: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), api.token) {
		t.Fatal("admin token leaked in public subscription")
	}
}

func TestAPIRejectsStaleConfigWrites(t *testing.T) {
	api := newTestAPI(t)
	response := api.request(t, http.MethodGet, "/api/v1/config", nil, api.token)
	if response.Code != http.StatusOK {
		t.Fatalf("get config: %d", response.Code)
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("config response has no ETag")
	}
	cfg := api.svc.Config()
	settings := cfg.Settings
	settings.LogLevel = "debug"
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Token", api.token)
	request.Header.Set("If-Match", etag)
	recorder := httptest.NewRecorder()
	api.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("current revision rejected: %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Token", api.token)
	request.Header.Set("If-Match", etag)
	recorder = httptest.NewRecorder()
	api.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected stale write to return 409, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"localmedia-unlock/internal/model"
	"localmedia-unlock/internal/service"
)

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func newClient(baseURL, token string) *client {
	return &client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *client) do(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-Admin-Token", c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	if target != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return e.Message
}

func decodeAPIError(response *http.Response) error {
	var value struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	_ = json.Unmarshal(data, &value)
	message := value.Error
	if message == "" {
		message = value.Message
	}
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = response.Status
	}
	return &apiError{Status: response.StatusCode, Message: message}
}

func (c *client) load(ctx context.Context) (model.Config, service.Status, []service.LogEntry, error) {
	var cfg model.Config
	if err := c.do(ctx, http.MethodGet, "/api/v1/config", nil, &cfg); err != nil {
		return cfg, service.Status{}, nil, err
	}
	var status service.Status
	if err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, &status); err != nil {
		return cfg, status, nil, err
	}
	var logs struct {
		Logs []service.LogEntry `json:"logs"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/logs?limit=40", nil, &logs); err != nil {
		return cfg, status, nil, err
	}
	return cfg, status, logs.Logs, nil
}

func (c *client) routes(ctx context.Context, routes []model.Route, custom []model.CustomRoute) error {
	body := map[string]any{"routes": routes}
	if custom != nil {
		body["customRoutes"] = custom
	}
	return c.do(ctx, http.MethodPut, "/api/v1/routes", body, nil)
}

func (c *client) action(ctx context.Context, action string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/controller/"+action, nil, nil)
}

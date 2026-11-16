package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"localmedia-unlock/internal/model"
	"localmedia-unlock/internal/service"
	"localmedia-unlock/web"
)

const maxBodySize = 2 << 20

type Server struct {
	svc      *service.Service
	mux      *http.ServeMux
	onChange func()
}

// NewServer wires the admin API. Public subscription content is protected by
// its own unguessable token and does not require the admin session.
func NewServer(svc *service.Service, onChange func()) http.Handler {
	s := &Server{
		svc:      svc,
		mux:      http.NewServeMux(),
		onChange: onChange,
	}
	s.routes()
	return s.securityHeaders(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	s.mux.HandleFunc("GET /api/v1/status", s.status)
	s.mux.HandleFunc("GET /api/v1/core", s.coreInfo)
	s.mux.HandleFunc("GET /api/v1/config", s.getConfig)
	s.mux.HandleFunc("GET /api/v1/presets", s.presets)
	s.mux.HandleFunc("GET /api/v1/outbounds", s.outbounds)
	s.mux.HandleFunc("POST /api/v1/outbounds", s.addOutbound)
	s.mux.HandleFunc("PUT /api/v1/outbounds/{id}", s.updateOutbound)
	s.mux.HandleFunc("DELETE /api/v1/outbounds/{id}", s.deleteOutbound)
	s.mux.HandleFunc("POST /api/v1/outbounds/import", s.importURI)
	s.mux.HandleFunc("POST /api/v1/outbounds/import-subscription", s.importSubscription)
	s.mux.HandleFunc("POST /api/v1/outbounds/{id}/test", s.testOutbound)
	s.mux.HandleFunc("GET /api/v1/routes", s.routesList)
	s.mux.HandleFunc("PUT /api/v1/routes", s.updateRoutes)
	s.mux.HandleFunc("GET /api/v1/inbounds", s.inboundsList)
	s.mux.HandleFunc("PUT /api/v1/inbounds", s.updateInbounds)
	s.mux.HandleFunc("GET /api/v1/settings", s.settings)
	s.mux.HandleFunc("PUT /api/v1/settings", s.updateSettings)
	s.mux.HandleFunc("GET /api/v1/config/export", s.exportConfig)
	s.mux.HandleFunc("POST /api/v1/config/import", s.importConfig)
	s.mux.HandleFunc("GET /api/v1/logs", s.logs)
	s.mux.HandleFunc("GET /api/v1/subscription", s.subscription)
	s.mux.HandleFunc("POST /api/v1/controller/start", s.start)
	s.mux.HandleFunc("POST /api/v1/controller/stop", s.stop)
	s.mux.HandleFunc("POST /api/v1/controller/restart", s.restart)
	s.mux.HandleFunc("POST /api/v1/controller/test", s.testRoute)
	// Compatibility aliases for the compact WebUI action paths.
	s.mux.HandleFunc("POST /api/v1/service/start", s.start)
	s.mux.HandleFunc("POST /api/v1/service/stop", s.stop)
	s.mux.HandleFunc("POST /api/v1/service/restart", s.restart)
	s.mux.HandleFunc("GET /sub/{token}", s.publicSubscription)
	s.mux.HandleFunc("GET /subscription/{token}", s.publicSubscription)
	s.mux.HandleFunc("GET /", s.index)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/sub/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) authStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := s.svc.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"authRequired":       s.svc.AuthRequired(),
		"subscriptionHost":   cfg.Settings.PublicHost,
		"subscriptionSigned": cfg.Settings.SubscriptionToken != "",
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if !s.svc.AuthRequired() {
		return true
	}
	token := r.Header.Get("X-Admin-Token")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if scheme, value, ok := strings.Cut(auth, " "); ok && strings.EqualFold(scheme, "Bearer") {
			token = strings.TrimSpace(value)
		}
	}
	return s.svc.Authenticate(token)
}

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "管理令牌无效，请重新登录")
			return
		}
		next(w, r)
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Status())
}

func (s *Server) coreInfo(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.svc.CoreInfo())
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	w.Header().Set("ETag", quoteETag(s.svc.ConfigRevision()))
	writeJSON(w, http.StatusOK, s.svc.Config())
}

func (s *Server) presets(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, model.Presets)
}

func (s *Server) outbounds(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	cfg := s.svc.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"outbounds": cfg.Outbounds,
	})
}

func (s *Server) addOutbound(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var node model.Outbound
	if err := decodeJSON(w, r, &node); err != nil {
		return
	}
	created, err := s.svc.AddOutbound(node)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateOutbound(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var node model.Outbound
	if err := decodeJSON(w, r, &node); err != nil {
		return
	}
	updated, err := s.svc.UpdateOutbound(r.PathValue("id"), node)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteOutbound(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	if err := s.svc.DeleteOutbound(r.PathValue("id")); err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) importURI(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var body struct {
		URI string `json:"uri"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	node, err := s.svc.ImportOutboundURI(body.URI)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) importSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	nodes, err := s.svc.ImportSubscription(body.Text)
	if err != nil && len(nodes) == 0 {
		s.handleError(w, err)
		return
	}
	s.changed()
	response := map[string]any{"outbounds": nodes, "count": len(nodes)}
	if err != nil {
		response["warning"] = err.Error()
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) testOutbound(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	latency, err := s.svc.TestOutbound(r.Context(), r.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"latencyMs": latency})
}

func (s *Server) routesList(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	cfg := s.svc.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"routes": cfg.Routes, "customRoutes": cfg.CustomRoutes,
	})
}

func (s *Server) updateRoutes(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var body struct {
		Routes       *[]model.Route       `json:"routes"`
		CustomRoutes *[]model.CustomRoute `json:"customRoutes"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	cfg := s.svc.Config()
	routes := cfg.Routes
	if body.Routes != nil {
		routes = *body.Routes
	}
	var custom []model.CustomRoute
	if body.CustomRoutes != nil {
		custom = *body.CustomRoutes
	}
	updated, err := s.svc.UpdateRoutes(routes, custom)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"routes": updated.Routes, "customRoutes": updated.CustomRoutes})
}

func (s *Server) inboundsList(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inbounds": s.svc.Config().Inbounds})
}

func (s *Server) updateInbounds(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var body struct {
		Inbounds []model.Inbound `json:"inbounds"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	cfg, err := s.svc.UpdateInbounds(body.Inbounds)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, map[string]any{"inbounds": cfg.Inbounds})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Config().Settings)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	var settings model.Settings
	if err := decodeJSON(w, r, &settings); err != nil {
		return
	}
	cfg, err := s.svc.UpdateSettings(settings)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, cfg.Settings)
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	data, err := s.svc.ExportConfig()
	if err != nil {
		s.handleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="localmedia-unlock.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.CheckConfigRevision(r.Header.Get("If-Match")); err != nil {
		s.handleError(w, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取导入内容失败: "+err.Error())
		return
	}
	cfg, err := s.svc.ImportConfig(data)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.changed()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, http.StatusOK, map[string]any{"logs": s.svc.Logs(limit)})
}

func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	base := requestBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":  s.svc.SubscriptionURL(base),
		"core": s.svc.Config().Settings.Core,
	})
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.Start(); err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Status())
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	err := s.svc.Stop()
	if err != nil && !errors.Is(err, service.ErrNotRunning) {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Status())
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	if err := s.svc.Restart(); err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Status())
}

func (s *Server) testRoute(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	latency, err := s.svc.TestDefaultRoute(r.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"latencyMs": latency})
}

func (s *Server) publicSubscription(w http.ResponseWriter, r *http.Request) {
	cfg := s.svc.Config()
	if !s.svc.SubscriptionTokenMatches(r.PathValue("token")) {
		http.NotFound(w, r)
		return
	}
	// Advertise a Clash-compatible YAML only when Mihomo is active, because
	// sing-box and Xray subscriptions have different document formats.
	if cfg.Settings.Core != model.CoreMihomo {
		writeError(w, http.StatusNotAcceptable, "当前内核不支持 Clash 订阅输出；请将内核切换为 Mihomo")
		return
	}
	data, err := s.svc.ClashSubscription()
	if err != nil {
		s.handleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Profile-Update-Interval", "12")
	w.Header().Set("Content-Disposition", `inline; filename="localmedia-unlock.yaml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *Server) handleError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, service.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, service.ErrInvalid), errors.Is(err, service.ErrDirectCannot):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	if status >= 500 {
		log.Printf("api error: %v", err)
	}
	writeError(w, status, err.Error())
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || path == "index.html" {
		path = "index.html"
	}
	data, err := web.FS.ReadFile(path)
	if err != nil {
		data, err = web.FS.ReadFile("index.html")
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "embedded web UI unavailable")
		return
	}
	switch {
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
	_, _ = w.Write(data)
}

func (s *Server) adminOK(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "管理令牌无效，请重新登录")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return errors.New("unsupported content type")
	}
	reader := http.MaxBytesReader(w, r.Body, maxBodySize)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "请求 JSON 无效: "+err.Error())
		return err
	}
	if decoder.More() {
		writeError(w, http.StatusBadRequest, "请求只能包含一个 JSON 对象")
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message, "message": message, "status": status})
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded == "https" || forwarded == "http" {
		scheme = forwarded
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func quoteETag(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + value + `"`
}

package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"localmedia-unlock/internal/compile"
	"localmedia-unlock/internal/config"
	"localmedia-unlock/internal/model"
	"localmedia-unlock/internal/outbound"
)

var (
	ErrUnauthorized  = errors.New("unauthorized")
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("invalid input")
	ErrConflict      = errors.New("conflict")
	ErrUnavailable   = errors.New("operation unavailable")
	ErrDirectCannot  = errors.New("direct outbound cannot be changed")
	defaultStopGrace = 5 * time.Second
)

type Service struct {
	store     *config.Store
	dataDir   string
	configDir string
	logs      *logRing
	process   *processManager
	mu        sync.Mutex
	version   string
}

type Status struct {
	Running       bool      `json:"running"`
	Core          string    `json:"core"`
	PID           int       `json:"pid,omitempty"`
	StartedAt     time.Time `json:"startedAt,omitempty"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	Version       string    `json:"version"`
	ConfigValid   bool      `json:"configValid"`
	ConfigError   string    `json:"configError,omitempty"`
	Outbounds     int       `json:"outbounds"`
	Routes        int       `json:"routes"`
}

type CreateOptions struct {
	ConfigPath string
	DataDir    string
	Version    string
}

func New(opts CreateOptions) (*Service, error) {
	dataDir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	store, err := config.NewStore(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	s := &Service{
		store:     store,
		dataDir:   dataDir,
		configDir: filepath.Join(dataDir, "runtime"),
		logs:      newLogRing(500),
		version:   opts.Version,
	}
	if _, _, err := store.Ensure(); err != nil {
		return nil, fmt.Errorf("initialize configuration: %w", err)
	}
	s.process = newProcessManager(s.configDir, func(level, source, message string) {
		s.logs.log(level, source, message)
	})
	s.logs.log("info", "controller", "configuration loaded from "+opts.ConfigPath)
	return s, nil
}

func (s *Service) ConfigPath() string {
	return s.store.Path()
}

func (s *Service) Authenticate(token string) bool {
	expected := s.store.Get().Settings.AdminToken
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (s *Service) SubscriptionTokenMatches(token string) bool {
	expected := s.store.Get().Settings.SubscriptionToken
	if expected == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (s *Service) SetAdminToken(token string) error {
	_, err := s.store.Update(func(cfg *model.Config) error {
		cfg.Settings.AdminToken = token
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func (s *Service) AuthRequired() bool {
	return s.store.Get().Settings.AdminToken != ""
}

func (s *Service) Config() model.Config {
	return s.store.Get().Sanitized()
}

func (s *Service) ConfigRevision() string {
	return s.store.ETag()
}

func (s *Service) CheckConfigRevision(revision string) error {
	if err := s.store.CheckRevision(revision); err != nil {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return nil
}

func (s *Service) Logs(limit int) []LogEntry {
	return s.logs.tail(limit)
}

func (s *Service) Status() Status {
	cfg := s.store.Get()
	core, started, pid := s.process.info()
	status := Status{
		Running:     s.process.running(),
		Core:        cfg.Settings.Core,
		PID:         pid,
		StartedAt:   started,
		Version:     s.version,
		ConfigValid: true,
		Outbounds:   len(cfg.Outbounds),
		Routes:      len(cfg.Routes),
	}
	if status.Running && !started.IsZero() {
		status.UptimeSeconds = int64(time.Since(started).Seconds())
	}
	if _, err := compile.Compile(cfg); err != nil {
		status.ConfigValid = false
		status.ConfigError = err.Error()
	}
	if core != "" {
		status.Core = core
	}
	return status
}

// CoreInfo reports whether a usable kernel binary is configured and its
// version. It deliberately does not start or modify anything.
func (s *Service) CoreInfo() map[string]any {
	cfg := s.store.Get()
	result := map[string]any{"core": cfg.Settings.Core}
	result["controllerVersion"] = s.version
	binary, err := s.kernelBinary(cfg.Settings.Core)
	if err != nil {
		result["available"] = false
		result["error"] = err.Error()
		return result
	}
	result["available"] = true
	result["binary"] = binary
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "version").CombinedOutput()
	if err != nil {
		// Mihomo supports -v rather than the subcommand used by the other
		// two kernels.
		output, err = exec.CommandContext(ctx, binary, "-v").CombinedOutput()
	}
	if err == nil {
		result["version"] = strings.TrimSpace(string(output))
	}
	return result
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked()
}

func (s *Service) startLocked() error {
	binary, configPath, err := s.prepareKernelLocked()
	if err != nil {
		return err
	}
	if err := s.process.start(binary, s.store.Get().Settings.Core, configPath); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// prepareKernelLocked compiles and validates a config without touching a
// running process. Restart uses it before stopping the old kernel so a bad
// edit cannot take the data plane down.
func (s *Service) prepareKernelLocked() (string, string, error) {
	cfg := s.store.Get()
	data, err := compile.Compile(cfg)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := os.MkdirAll(s.configDir, 0o750); err != nil {
		return "", "", fmt.Errorf("create runtime directory: %w", err)
	}
	configPath := filepath.Join(s.configDir, cfg.Settings.Core+".config.json")
	if err := writeFileAtomic(configPath, data, 0o600); err != nil {
		return "", "", fmt.Errorf("write kernel config: %w", err)
	}
	binary, err := s.kernelBinary(cfg.Settings.Core)
	if err != nil {
		return "", "", err
	}
	if err := s.validateKernelConfig(binary, cfg.Settings.Core, configPath); err != nil {
		return "", "", err
	}
	return binary, configPath, nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.process.stop(defaultStopGrace)
	if errors.Is(err, ErrNotRunning) {
		return ErrNotRunning
	}
	return err
}

func (s *Service) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	binary, configPath, err := s.prepareKernelLocked()
	if err != nil {
		return err
	}
	if s.process.running() {
		err := s.process.stop(defaultStopGrace)
		if err != nil && !errors.Is(err, ErrNotRunning) {
			return err
		}
	}
	if err := s.process.start(binary, s.store.Get().Settings.Core, configPath); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

func (s *Service) kernelBinary(core string) (string, error) {
	cfg := s.store.Get()
	envNames := map[string]string{
		model.CoreMihomo:  "LOCALMEDIA_MIHOMO_BIN",
		model.CoreSingBox: "LOCALMEDIA_SINGBOX_BIN",
		model.CoreXray:    "LOCALMEDIA_XRAY_BIN",
	}
	if path := os.Getenv(envNames[core]); path != "" {
		return path, nil
	}
	if path := cfg.Settings.KernelPaths[core]; path != "" {
		return path, nil
	}
	defaults := map[string][]string{
		model.CoreMihomo:  {"mihomo"},
		model.CoreSingBox: {"sing-box"},
		model.CoreXray:    {"xray"},
	}
	for _, name := range defaults[core] {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: %s binary not found; install it or set %s", ErrUnavailable, core, envNames[core])
}

func (s *Service) validateKernelConfig(binary, core, configPath string) error {
	var args []string
	switch core {
	case model.CoreMihomo:
		args = []string{"-t", "-f", configPath}
	case model.CoreSingBox:
		args = []string{"check", "-c", configPath}
	case model.CoreXray:
		args = []string{"run", "-test", "-config", configPath}
	default:
		return fmt.Errorf("%w: unsupported core %q", ErrInvalid, core)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("%w: validating %s config timed out", ErrUnavailable, core)
	}
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%w: %s rejected the generated config: %s", ErrInvalid, core, message)
	}
	return nil
}

func (s *Service) ReplaceConfig(cfg model.Config) (model.Config, error) {
	cfg.InitSecrets()
	if err := s.store.Save(cfg); err != nil {
		return model.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.logs.log("info", "controller", "configuration replaced")
	return s.Config(), nil
}

func (s *Service) UpdateRoutes(routes []model.Route, custom []model.CustomRoute) (model.Config, error) {
	cfg, err := s.store.Update(func(cfg *model.Config) error {
		cfg.Routes = routes
		if custom != nil {
			cfg.CustomRoutes = custom
		}
		return nil
	})
	if err != nil {
		return model.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.logs.log("info", "controller", "routing rules updated")
	if err := s.reloadIfRunning(); err != nil {
		return model.Config{}, err
	}
	return cfg.Sanitized(), nil
}

func (s *Service) UpdateSettings(settings model.Settings) (model.Config, error) {
	cfg, err := s.store.Update(func(cfg *model.Config) error {
		current := cfg.Settings
		if settings.AdminToken == "" {
			settings.AdminToken = current.AdminToken
		}
		if settings.SubscriptionToken == "" {
			settings.SubscriptionToken = current.SubscriptionToken
		}
		if settings.Listen == "" {
			settings.Listen = current.Listen
		}
		cfg.Settings = settings
		return nil
	})
	if err != nil {
		return model.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.logs.log("info", "controller", "settings updated")
	if err := s.reloadIfRunning(); err != nil {
		return model.Config{}, err
	}
	return cfg.Sanitized(), nil
}

func (s *Service) UpdateInbounds(inbounds []model.Inbound) (model.Config, error) {
	cfg, err := s.store.Update(func(cfg *model.Config) error {
		cfg.Inbounds = inbounds
		return nil
	})
	if err != nil {
		return model.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.logs.log("info", "controller", "inbounds updated")
	if err := s.reloadIfRunning(); err != nil {
		return model.Config{}, err
	}
	return cfg.Sanitized(), nil
}

func (s *Service) AddOutbound(node model.Outbound) (model.Outbound, error) {
	node.ID = strings.TrimSpace(node.ID)
	if node.ID == "" {
		node.ID = model.NewID("out")
	}
	var created model.Outbound
	_, err := s.store.Update(func(cfg *model.Config) error {
		for _, existing := range cfg.Outbounds {
			if existing.ID == node.ID {
				return fmt.Errorf("%w: outbound id %q already exists", ErrConflict, node.ID)
			}
			if strings.EqualFold(existing.Name, node.Name) {
				return fmt.Errorf("%w: outbound name %q already exists", ErrConflict, node.Name)
			}
		}
		cfg.Outbounds = append(cfg.Outbounds, node)
		created = node
		return nil
	})
	if err != nil {
		return model.Outbound{}, wrapInvalid(err)
	}
	s.logs.log("info", "controller", "outbound added: "+node.Name)
	if err := s.reloadIfRunning(); err != nil {
		return model.Outbound{}, err
	}
	return created, nil
}

func (s *Service) UpdateOutbound(id string, node model.Outbound) (model.Outbound, error) {
	if id == model.DirectOutboundID {
		return model.Outbound{}, ErrDirectCannot
	}
	node.ID = id
	var updated model.Outbound
	_, err := s.store.Update(func(cfg *model.Config) error {
		found := false
		for index, existing := range cfg.Outbounds {
			if existing.ID == id {
				found = true
				cfg.Outbounds[index] = node
			} else if strings.EqualFold(existing.Name, node.Name) {
				return fmt.Errorf("%w: outbound name %q already exists", ErrConflict, node.Name)
			}
		}
		if !found {
			return fmt.Errorf("%w: outbound %q", ErrNotFound, id)
		}
		updated = node
		return nil
	})
	if err != nil {
		return model.Outbound{}, wrapInvalid(err)
	}
	s.logs.log("info", "controller", "outbound updated: "+node.Name)
	if err := s.reloadIfRunning(); err != nil {
		return model.Outbound{}, err
	}
	return updated, nil
}

func (s *Service) DeleteOutbound(id string) error {
	if id == model.DirectOutboundID {
		return ErrDirectCannot
	}
	cfg := s.store.Get()
	for _, route := range cfg.Routes {
		if route.Outbound == id {
			return fmt.Errorf("%w: outbound is used by route %q", ErrConflict, route.Service)
		}
	}
	for _, route := range cfg.CustomRoutes {
		if route.Outbound == id {
			return fmt.Errorf("%w: outbound is used by custom route %q", ErrConflict, route.ID)
		}
	}
	_, err := s.store.Update(func(cfg *model.Config) error {
		if cfg.Settings.DefaultOutbound == id {
			return fmt.Errorf("%w: outbound is the default outbound", ErrConflict)
		}
		filtered := cfg.Outbounds[:0]
		found := false
		for _, existing := range cfg.Outbounds {
			if existing.ID == id {
				found = true
				continue
			}
			filtered = append(filtered, existing)
		}
		if !found {
			return fmt.Errorf("%w: outbound %q", ErrNotFound, id)
		}
		cfg.Outbounds = filtered
		return nil
	})
	if err != nil {
		return wrapInvalid(err)
	}
	s.logs.log("info", "controller", "outbound deleted: "+id)
	if err := s.reloadIfRunning(); err != nil {
		return err
	}
	return nil
}

// reloadIfRunning restarts an already-running kernel so route and node edits
// take effect immediately. The replacement config is validated first.
func (s *Service) reloadIfRunning() error {
	if !s.process.running() {
		return nil
	}
	if err := s.Restart(); err != nil {
		s.logs.log("error", "controller", "kernel reload failed: "+err.Error())
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.logs.log("info", "controller", "kernel reloaded with updated configuration")
	return nil
}

func (s *Service) ImportOutboundURI(uri string) (model.Outbound, error) {
	node, err := outbound.ParseURI(uri)
	if err != nil {
		return model.Outbound{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.AddOutbound(node)
}

func (s *Service) ImportSubscription(text string) ([]model.Outbound, error) {
	nodes, parseErr := outbound.ParseSubscription(text)
	if len(nodes) == 0 {
		if parseErr == nil {
			parseErr = outbound.ErrNoNodes
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, parseErr)
	}
	added := make([]model.Outbound, 0, len(nodes))
	_, err := s.store.Update(func(cfg *model.Config) error {
		for _, node := range nodes {
			node.Name = uniqueName(*cfg, node.Name)
			for _, existing := range cfg.Outbounds {
				if existing.ID == node.ID {
					node.ID = model.NewID("out")
					break
				}
			}
			cfg.Outbounds = append(cfg.Outbounds, node)
			added = append(added, node)
		}
		return nil
	})
	if err != nil {
		return added, fmt.Errorf("imported %d of %d nodes: %w", len(added), len(nodes), wrapInvalid(err))
	}
	s.logs.log("info", "controller", fmt.Sprintf("imported %d nodes", len(added)))
	if err := s.reloadIfRunning(); err != nil {
		return added, err
	}
	if parseErr != nil {
		s.logs.log("warn", "controller", parseErr.Error())
	}
	return added, nil
}

func uniqueName(cfg model.Config, name string) string {
	used := map[string]bool{"direct": true}
	for _, node := range cfg.Outbounds {
		used[strings.ToLower(node.Name)] = true
	}
	if !used[strings.ToLower(strings.TrimSpace(name))] {
		return name
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s (%d)", name, index)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

// TestOutbound measures TCP reachability. It does not claim to verify the
// remote proxy protocol or streaming unlock result.
func (s *Service) TestOutbound(ctx context.Context, id string) (int64, error) {
	node, ok := s.store.Get().OutboundByID(id)
	if !ok {
		return 0, fmt.Errorf("%w: outbound %q", ErrNotFound, id)
	}
	if id == model.DirectOutboundID {
		return 0, fmt.Errorf("%w: direct outbound cannot be probed", ErrInvalid)
	}
	start := time.Now()
	dialer := &simpleDialer{timeout: 8 * time.Second}
	if err := dialer.dial(ctx, node.Server, node.Port); err != nil {
		return 0, fmt.Errorf("connect to %s:%d: %w", node.Server, node.Port, err)
	}
	return time.Since(start).Milliseconds(), nil
}

func (s *Service) TestDefaultRoute(ctx context.Context) (int64, error) {
	cfg := s.store.Get()
	enabled := cfg.EnabledInbounds()
	if len(enabled) == 0 {
		return 0, fmt.Errorf("%w: no enabled inbound", ErrInvalid)
	}
	inbound := enabled[0]
	address := inbound.Listen
	if address == "0.0.0.0" || address == "::" {
		address = "127.0.0.1"
	}
	proxyAddr := fmt.Sprintf("%s:%d", address, inbound.Port)
	start := time.Now()
	client := newProbeClient(proxyAddr, cfg, 12*time.Second)
	if err := probeHTTP(ctx, client, cfg.Settings.ProbeURL); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}

func (s *Service) ExportConfig() ([]byte, error) {
	return json.MarshalIndent(s.store.Get(), "", "  ")
}

func (s *Service) ImportConfig(data []byte) (model.Config, error) {
	cfg, err := s.store.ImportJSON(data)
	if err != nil {
		return model.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.logs.log("info", "controller", "configuration imported")
	if err := s.reloadIfRunning(); err != nil {
		return model.Config{}, err
	}
	return cfg.Sanitized(), nil
}

func (s *Service) SubscriptionURL(base string) string {
	cfg := s.store.Get()
	base = strings.TrimRight(firstNonEmpty(cfg.Settings.SubscriptionBaseURL, base), "/")
	if base == "" {
		base = "http://" + cfg.Settings.Listen
	}
	return base + "/sub/" + cfg.Settings.SubscriptionToken
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kernel-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func wrapInvalid(err error) error {
	if err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrInvalid, err)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

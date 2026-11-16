package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"localmedia-unlock/internal/model"
)

var ErrNotConfigured = errors.New("configuration file does not exist")

// Store persists a complete configuration atomically. Secrets stay on disk;
// API callers receive a sanitized copy through the service layer.
type Store struct {
	mu       sync.RWMutex
	path     string
	cfg      model.Config
	revision string
}

func NewStore(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	path = absolute
	store := &Store{path: path}
	if err := store.Load(); err != nil && !errors.Is(err, ErrNotConfigured) {
		return nil, err
	}
	return store, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotConfigured
	}
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	s.revision = revisionFor(data)
	s.mu.Unlock()
	return nil
}

// Ensure creates a default configuration on first run. It returns true when a
// new file was written.
func (s *Store) Ensure() (model.Config, bool, error) {
	s.mu.RLock()
	cfg := cloneConfig(s.cfg)
	s.mu.RUnlock()
	if cfg.SchemaVersion != 0 {
		return cfg, false, nil
	}
	cfg = model.DefaultConfig()
	cfg.InitSecrets()
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return model.Config{}, false, err
	}
	if err := s.Save(cfg); err != nil {
		return model.Config{}, false, err
	}
	return cfg, true, nil
}

func (s *Store) Get() model.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}

// Save validates before writing. Atomic rename prevents a crash from leaving a
// truncated configuration behind.
func (s *Store) Save(cfg model.Config) error {
	cfg.SchemaVersion = model.SchemaVersion
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	// Best effort on platforms/filesystems that do not support directory sync.
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	s.mu.Lock()
	s.cfg = cfg
	s.revision = revisionFor(data)
	s.mu.Unlock()
	return nil
}

func (s *Store) Update(mutator func(*model.Config) error) (model.Config, error) {
	s.mu.Lock()
	cfg := cloneConfig(s.cfg)
	s.mu.Unlock()
	if err := mutator(&cfg); err != nil {
		return model.Config{}, err
	}
	cfg.Normalize()
	if err := s.Save(cfg); err != nil {
		return model.Config{}, err
	}
	return cfg, nil
}

// ImportJSON replaces the active configuration after validating it.
func (s *Store) ImportJSON(data []byte) (model.Config, error) {
	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return model.Config{}, fmt.Errorf("decode imported config: %w", err)
	}
	cfg.Normalize()
	cfg.InitSecrets()
	if err := s.Save(cfg); err != nil {
		return model.Config{}, err
	}
	return cfg, nil
}

func cloneConfig(cfg model.Config) model.Config {
	data, err := json.Marshal(cfg)
	if err != nil {
		panic("model.Config must be JSON encodable: " + err.Error())
	}
	var result model.Config
	if err := json.Unmarshal(data, &result); err != nil {
		panic("model.Config must be JSON decodable: " + err.Error())
	}
	return result
}

// expectedRevision returns the full file ETag. It is exposed to clients to
// avoid silently overwriting a newer route or node edit from another tab.
func (s *Store) ETag() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

// CheckRevision accepts an empty If-Match for backward compatibility, or the
// exact current revision. This is intentionally conservative: a stale writer
// must reload instead of overwriting a newer configuration.
func (s *Store) CheckRevision(revision string) error {
	revision = strings.Trim(strings.TrimSpace(revision), `"`)
	if revision == "" || revision == "*" {
		return nil
	}
	current := strings.Trim(s.ETag(), `"`)
	if subtle.ConstantTimeCompare([]byte(revision), []byte(current)) != 1 {
		return fmt.Errorf("configuration changed; reload before saving")
	}
	return nil
}

func revisionFor(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

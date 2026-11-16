package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"localmedia-unlock/internal/model"
)

func TestEnsurePersistsSecretsAndConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, created, err := store.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected initial configuration to be created")
	}
	if cfg.Settings.AdminToken == "" || cfg.Settings.ProxyPassword == "" {
		t.Fatal("generated secrets are empty")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("expected 0600 config permissions, got %o", info.Mode().Perm())
		}
	}

	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get().Settings.AdminToken; got != cfg.Settings.AdminToken {
		t.Fatal("secret was not persisted")
	}
}

func TestSaveRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	cfg.Settings.DefaultOutbound = "missing"
	cfg.InitSecrets()
	if err := store.Save(cfg); err == nil {
		t.Fatal("expected invalid configuration to be rejected")
	}
}

func TestRevisionChangesAndRejectsStaleWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := store.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	first := store.ETag()
	if first == "" {
		t.Fatal("expected a revision")
	}
	if err := store.CheckRevision(first); err != nil {
		t.Fatal(err)
	}
	cfg.Settings.LogLevel = "debug"
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	second := store.ETag()
	if second == first {
		t.Fatal("revision did not change after a save")
	}
	if err := store.CheckRevision(first); err == nil {
		t.Fatal("expected stale revision to be rejected")
	}
}

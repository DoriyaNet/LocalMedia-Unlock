package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"localmedia-unlock/internal/model"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	s, err := New(CreateOptions{
		ConfigPath: filepath.Join(dir, "config.json"),
		DataDir:    dir,
		Version:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestServiceAuthAndSanitization(t *testing.T) {
	s := newTestService(t)
	token := s.store.Get().Settings.AdminToken
	if !s.Authenticate(token) {
		t.Fatal("expected generated token to authenticate")
	}
	if s.Authenticate("wrong") {
		t.Fatal("wrong token authenticated")
	}
	if s.Config().Settings.AdminToken != "" {
		t.Fatal("admin token exposed by API config")
	}
}

func TestOutboundLifecycle(t *testing.T) {
	s := newTestService(t)
	node := model.Outbound{
		Name: "Test", Type: "trojan", Server: "example.com", Port: 443,
		Password: "secret", Enabled: true, Network: "tcp",
	}
	created, err := s.AddOutbound(node)
	if err != nil {
		t.Fatal(err)
	}
	routes := s.store.Get().Routes
	routes[0].Outbound = created.ID
	if _, err := s.UpdateRoutes(routes, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOutbound(created.ID); err == nil {
		t.Fatal("expected deletion of routed outbound to fail")
	}
	routes[0].Outbound = model.DirectOutboundID
	if _, err := s.UpdateRoutes(routes, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOutbound(created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestImportConfigRejectsGarbage(t *testing.T) {
	s := newTestService(t)
	if _, err := s.ImportConfig([]byte("not json")); err == nil {
		t.Fatal("expected garbage import to fail")
	}
}

func TestTestOutboundRejectsMissing(t *testing.T) {
	s := newTestService(t)
	if _, err := s.TestOutbound(context.Background(), "missing"); err == nil {
		t.Fatal("expected missing outbound to fail")
	}
}

func TestKernelArgs(t *testing.T) {
	args, err := kernelArgs(model.CoreSingBox, "/tmp/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) == 0 || args[0] != "run" {
		t.Fatalf("unexpected sing-box args: %v", args)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.json")
	if err := writeFileAtomic(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{}\n" {
		t.Fatalf("unexpected file: %q, %v", data, err)
	}
}

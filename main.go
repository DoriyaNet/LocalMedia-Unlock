package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"localmedia-unlock/internal/api"
	"localmedia-unlock/internal/service"
	"localmedia-unlock/internal/tui"
)

var version = "dev"

const defaultDataDir = "/var/lib/localmedia-unlock"

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "tui":
		return tui.Run(args[1:], version)
	case "version", "-version", "--version":
		fmt.Printf("localmedia-unlock %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q; run 'localmedia-unlock help'", args[0])
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dataDir := flags.String("data-dir", envOr("LOCALMEDIA_DATA_DIR", defaultDataDir), "state and runtime directory")
	configPath := flags.String("config", "", "configuration path (default DATA_DIR/config.json)")
	listen := flags.String("listen", envOr("LOCALMEDIA_LISTEN", ""), "management API listen address (overrides config)")
	adminToken := flags.String("token", os.Getenv("LOCALMEDIA_ADMIN_TOKEN"), "admin token (overrides config; use a file or service environment)")
	tokenFile := flags.String("token-file", os.Getenv("LOCALMEDIA_ADMIN_TOKEN_FILE"), "read admin token from this file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			return fmt.Errorf("read token file: %w", err)
		}
		*adminToken = strings.TrimSpace(string(data))
	}
	if *configPath == "" {
		*configPath = filepath.Join(*dataDir, "config.json")
	}
	svc, err := service.New(service.CreateOptions{
		ConfigPath: *configPath,
		DataDir:    *dataDir,
		Version:    version,
	})
	if err != nil {
		return err
	}
	if *adminToken != "" {
		if err := svc.SetAdminToken(*adminToken); err != nil {
			return fmt.Errorf("apply admin token override: %w", err)
		}
	}
	config := svc.Config()
	address := config.Settings.Listen
	if *listen != "" {
		address = *listen
	}
	if address == "" {
		address = "127.0.0.1:9090"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server := &http.Server{
		Addr:              address,
		Handler:           api.NewServer(svc, nil),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("localmedia-unlock %s listening on http://%s (data: %s)", version, address, *dataDir)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	if autoStart := svc.Config().Settings.AutoStart; autoStart {
		go func() {
			if err := svc.Start(); err != nil {
				log.Printf("auto-start failed: %v", err)
			}
		}()
	}
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("management server: %w", err)
		}
	case <-ctx.Done():
		log.Printf("shutting down")
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	if err := svc.Stop(); err != nil && !errors.Is(err, service.ErrNotRunning) {
		log.Printf("kernel shutdown: %v", err)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func printUsage() {
	fmt.Print(`LocalMedia Unlock - 流媒体分流解锁控制器

用法:
  localmedia-unlock serve [--data-dir DIR] [--config FILE] [--listen ADDR]
  localmedia-unlock tui   [--api URL] [--token TOKEN]
  localmedia-unlock version

内核路径:
  LOCALMEDIA_MIHOMO_BIN, LOCALMEDIA_SINGBOX_BIN, LOCALMEDIA_XRAY_BIN

注意: serve 默认把管理 API 绑定在 127.0.0.1:9090。通过 SSH 转发或
带 TLS 的反向代理访问；不要把管理面板直接暴露到公网。
`)
}

package service

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"localmedia-unlock/internal/model"
)

type simpleDialer struct {
	timeout time.Duration
}

func (d *simpleDialer) dial(ctx context.Context, host string, port int) error {
	dialer := net.Dialer{Timeout: d.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return err
	}
	return conn.Close()
}

// newProbeClient performs one bounded GET through the local proxy. Mihomo and
// sing-box expose an HTTP-capable mixed listener; Xray's generated mixed mode
// is represented by SOCKS on the configured port and HTTP on port+1.
func newProbeClient(proxyAddr string, cfg model.Config, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		DisableKeepAlives: true,
	}
	if cfg.Settings.ProxyAuthEnabled {
		transport.Proxy = http.ProxyURL(&url.URL{
			Scheme: "http", Host: proxyAddr,
			User: url.UserPassword(cfg.Settings.ProxyUsername, cfg.Settings.ProxyPassword),
		})
	}
	// Xray has no native mixed listener: its SOCKS inbound occupies the
	// configured port and HTTP uses port+1. Probe the SOCKS port directly.
	enabled := cfg.EnabledInbounds()
	xrayMixed := cfg.Settings.Core == model.CoreXray && len(enabled) > 0 && enabled[0].Type == "mixed"
	if xrayMixed || inboundIsSOCKSOnly(cfg) {
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialSOCKS5(ctx, proxyAddr, cfg.Settings.ProxyUsername, cfg.Settings.ProxyPassword, address, timeout)
		}
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func inboundIsSOCKSOnly(cfg model.Config) bool {
	enabled := cfg.EnabledInbounds()
	if len(enabled) == 0 {
		return false
	}
	// Xray's mixed listener is represented internally as separate socks+http
	// inbounds; only a socks-only controller config needs the SOCKS dialer.
	for _, inbound := range enabled {
		if inbound.Type == "mixed" || inbound.Type == "http" {
			return false
		}
	}
	return enabled[0].Type == "socks"
}

func probeHTTP(ctx context.Context, c *http.Client, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := c.Do(request)
	if err != nil {
		return fmt.Errorf("probe through inbound: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode >= 400 {
		return fmt.Errorf("probe returned HTTP %d", response.StatusCode)
	}
	return nil
}

func dialSOCKS5(ctx context.Context, proxyAddr, username, password, target string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)
	methods := []byte{0x00}
	if username != "" || password != "" {
		methods = []byte{0x00, 0x02}
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return closeOnError(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return closeOnError(err)
	}
	if reply[0] != 0x05 {
		return closeOnError(fmt.Errorf("invalid SOCKS5 version %d", reply[0]))
	}
	if reply[1] == 0x02 {
		if len(username) > 255 || len(password) > 255 {
			return closeOnError(fmt.Errorf("SOCKS5 credentials are too long"))
		}
		auth := []byte{0x01, byte(len(username))}
		auth = append(auth, username...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, password...)
		if _, err := conn.Write(auth); err != nil {
			return closeOnError(err)
		}
		if _, err := io.ReadFull(conn, reply); err != nil {
			return closeOnError(err)
		}
		if reply[1] != 0x00 {
			return closeOnError(fmt.Errorf("SOCKS5 authentication failed"))
		}
	} else if reply[1] != 0x00 {
		return closeOnError(fmt.Errorf("SOCKS5 server has no supported auth method"))
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return closeOnError(err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		return closeOnError(err)
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return closeOnError(fmt.Errorf("SOCKS5 target host is too long"))
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = append(request, byte(port>>8), byte(port))
	if _, err := conn.Write(request); err != nil {
		return closeOnError(err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return closeOnError(err)
	}
	if header[1] != 0x00 {
		return closeOnError(fmt.Errorf("SOCKS5 connect failed with code %d", header[1]))
	}
	var skip int
	switch header[3] {
	case 0x01:
		skip = 4 + 2
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return closeOnError(err)
		}
		skip = int(length[0]) + 2
	case 0x04:
		skip = 16 + 2
	default:
		return closeOnError(fmt.Errorf("invalid SOCKS5 address type %d", header[3]))
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip)); err != nil {
		return closeOnError(err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// Keep imports used by implementations that need streaming proxy parsers.
var _ = bufio.NewReader
var _ = base64.RawStdEncoding
var _ = strings.TrimSpace

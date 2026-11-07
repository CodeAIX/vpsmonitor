package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const ConnectPath = "/api/vpsmonitor/agent-connect"
const ReadyPath = "/api/vpsmonitor/agent-ready"
const ReadyHeader = "X-Vpsmonitor-Ingress"

var ipv4 = netip.MustParsePrefix("100.64.0.0/10")
var ipv6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

func contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	return ipv4.Contains(addr) || ipv6.Contains(addr)
}

// Origin is the dedicated Agent endpoint, separate from the web app URL.
func Origin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("Agent URL must be a Tailnet HTTP(S) origin with no credentials, path or query")
	}
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && !contains(addr) {
		return nil, errors.New("Agent URL IP is outside the Tailnet address ranges")
	}
	return u, nil
}

// DialContext resolves once and binds every candidate socket to tailscale0.
// Address ranges alone are insufficient because ordinary networks can use CGNAT.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	iface, err := net.InterfaceByName("tailscale0")
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return nil, errors.New("tailscale0 is unavailable, check host Tailscale and Docker host networking")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid Agent endpoint address")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("cannot resolve Hub Tailnet address")
	}
	dialer := net.Dialer{Control: bindSocket, Timeout: 3 * time.Second}
	var dialErr error
	for _, addr := range addresses {
		if !contains(addr) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr == nil {
		return nil, errors.New("Hub name did not resolve to a Tailnet address")
	}
	return nil, fmt.Errorf("cannot connect through tailscale0, check Hub port and Tailnet grants: %w", dialErr)
}

// Probe exercises the same route as the Agent and never sends a token.
func Probe(ctx context.Context, raw string) error {
	u, err := Origin(raw)
	if err != nil {
		return err
	}
	u.Path = ReadyPath
	transport := &http.Transport{DialContext: DialContext, TLSHandshakeTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get(ReadyHeader) != "agent" {
		return fmt.Errorf("HTTP %d from target, expected the dedicated Agent ingress", resp.StatusCode)
	}
	return nil
}

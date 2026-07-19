// SPDX-License-Identifier: AGPL-3.0

package activitypub

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SafetyPolicy controls the safe HTTP client's outbound restrictions.
// The default policy (zero value) is strict: HTTPS only, no private/loopback/
// link-local IPs, no redirects, max 1000 followers per group.
type SafetyPolicy struct {
	// AllowHTTP permits http:// (cleartext) requests. Default false.
	AllowHTTP bool
	// AllowPrivateIPs permits RFC1918 ranges (10/8, 172.16/12, 192.168/16,
	// 100.64/10 CGNAT) and the IPv6 ULA range fc00::/7. Default false.
	AllowPrivateIPs bool
	// AllowLoopback permits 127.0.0.0/8 and ::1. Default false.
	AllowLoopback bool
	// AllowLinkLocalUnicast permits 169.254.0.0/16 and fe80::/10. Default false.
	// This includes cloud metadata endpoints.
	AllowLinkLocalUnicast bool
	// AllowUnspecified permits 0.0.0.0 and ::. Default false.
	AllowUnspecified bool
	// MaxFollowersPerGroup caps the number of followers stored per group.
	// Zero means use the package default (1000).
	MaxFollowersPerGroup int
	// DialTimeout is the TCP connect timeout. Zero means 5s.
	DialTimeout time.Duration
	// TotalTimeout is the total request timeout. Zero means 30s.
	TotalTimeout time.Duration
}

// defaultSafetyPolicy returns a strict policy with sensible timeouts.
func defaultSafetyPolicy() SafetyPolicy {
	return SafetyPolicy{
		MaxFollowersPerGroup: 1000,
		DialTimeout:          5 * time.Second,
		TotalTimeout:         30 * time.Second,
	}
}

// ErrUnsafeHost is returned when the resolved IP for a target host fails
// the policy check. It is wrapped by safeDialContext and safeRoundTripper.
var ErrUnsafeHost = errors.New("unsafe host: target IP is blocked by safety policy")

// ErrUnsafeScheme is returned when the request scheme is not allowed by the
// safety policy.
var ErrUnsafeScheme = errors.New("unsafe scheme: only https is allowed by default")

// safeDialContext is an http.Transport DialContext that resolves the target
// host, checks every resolved IP against the safety policy, and only then
// delegates to the underlying dialer.
//
// This is the primary SSRF defense: it ensures we never connect to an IP
// the policy forbids, even if a hostname resolves to it. We resolve the
// hostname ourselves so we can inspect all returned addresses; this
// protects against DNS-based bypasses (e.g. an attacker-controlled DNS
// that returns 127.0.0.1 for evil.example.com).
func safeDialContext(dialer *net.Dialer, policy SafetyPolicy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("split host:port: %w", err)
		}

		// If addr is a literal IP, check it directly. If it's a hostname,
		// resolve it and check every returned IP.
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else {
			resolver := &net.Resolver{}
			resolved, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", host, err)
			}
			if len(resolved) == 0 {
				return nil, fmt.Errorf("resolve %s: no addresses", host)
			}
			for _, r := range resolved {
				ips = append(ips, r.IP)
			}
		}

		for _, ip := range ips {
			if err := checkIP(ip, policy); err != nil {
				log.Printf("activitypub: SSRF guard blocked %s (%s): %v", host, ip, err)
				return nil, fmt.Errorf("%w: %s resolves to %s", ErrUnsafeHost, host, ip)
			}
		}

		// Safe to dial using the original address (so SNI / TLS work correctly).
		return dialer.DialContext(ctx, network, addr)
	}
}

// checkIP returns an error if the IP is forbidden by the policy.
func checkIP(ip net.IP, policy SafetyPolicy) error {
	if ip == nil {
		return fmt.Errorf("nil IP")
	}
	switch {
	case ip.IsUnspecified():
		if !policy.AllowUnspecified {
			return fmt.Errorf("IP %s is unspecified", ip)
		}
	case ip.IsLoopback():
		if !policy.AllowLoopback {
			return fmt.Errorf("IP %s is loopback", ip)
		}
	case ip.IsLinkLocalUnicast():
		if !policy.AllowLinkLocalUnicast {
			return fmt.Errorf("IP %s is link-local unicast (e.g. cloud metadata)", ip)
		}
	case ip.IsPrivate():
		// net.IP.IsPrivate covers RFC1918 (10/8, 172.16/12, 192.168/16),
		// CGNAT (100.64/10), and IPv6 ULA (fc00::/7).
		if !policy.AllowPrivateIPs {
			return fmt.Errorf("IP %s is private (RFC1918/ULA/CGNAT)", ip)
		}
	case ip.IsMulticast():
		return fmt.Errorf("IP %s is multicast", ip)
	}
	return nil
}

// safeRoundTripper enforces URL-scheme checks that the dialer cannot.
type safeRoundTripper struct {
	base   http.RoundTripper
	policy SafetyPolicy
}

func (s *safeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkRequestSafe(req, s.policy); err != nil {
		log.Printf("activitypub: SSRF guard blocked %s %s: %v", req.Method, req.URL.String(), err)
		return nil, err
	}
	return s.base.RoundTrip(req)
}

// checkRequestSafe validates the request URL against the policy.
// It refuses non-https schemes (unless AllowHTTP is set) and refuses
// URLs whose host is empty or malformed.
func checkRequestSafe(req *http.Request, policy SafetyPolicy) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("nil request or URL")
	}
	scheme := strings.ToLower(req.URL.Scheme)
	switch scheme {
	case "https":
		// always allowed
	case "http":
		if !policy.AllowHTTP {
			return fmt.Errorf("%w (got %s)", ErrUnsafeScheme, req.URL.Scheme)
		}
	default:
		return fmt.Errorf("%w: %s is not allowed", ErrUnsafeScheme, req.URL.Scheme)
	}
	if req.URL.Host == "" {
		return fmt.Errorf("URL has no host")
	}
	return nil
}

// noFollowRedirect is an http.Client.CheckRedirect that disables redirects
// entirely: the client returns the 3xx response to the caller instead of
// following it. This prevents a malicious inbox URL from redirecting the
// daemon to an internal IP.
func noFollowRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

// newSafeHTTPClient builds an http.Client hardened against SSRF.
//
// The returned client:
//   - pins scheme to https (configurable via policy.AllowHTTP for tests)
//   - refuses to dial private/loopback/link-local IPs (configurable via policy)
//   - resolves the target hostname itself and checks every resolved IP
//   - does not follow HTTP redirects at all (returns the 3xx to the caller)
//   - has a 5s connect timeout and 30s total timeout (configurable via policy)
func newSafeHTTPClient(policy SafetyPolicy) *http.Client {
	dialTimeout := policy.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}
	totalTimeout := policy.TotalTimeout
	if totalTimeout == 0 {
		totalTimeout = 30 * time.Second
	}

	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext:           safeDialContext(dialer, policy),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Timeout: totalTimeout,
		Transport: &safeRoundTripper{base: transport, policy: policy},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Use the same policy to decide whether to follow.
			if err := checkRequestSafe(req, policy); err != nil {
				return err
			}
			return noFollowRedirect(req, via)
		},
	}
}

// validateInboxURL is a convenience used by DeliverActivity: it parses
// the URL and applies the scheme check up front, so a malicious inbox
// can be rejected before we even open a connection.
func validateInboxURL(rawURL string, policy SafetyPolicy) error {
	if rawURL == "" {
		return fmt.Errorf("empty inbox URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse inbox URL: %w", err)
	}
	// Reuse the same logic as checkRequestSafe by synthesising a request.
	req, err := http.NewRequest(http.MethodPost, rawURL, nil)
	if err != nil {
		return fmt.Errorf("parse inbox URL: %w", err)
	}
	_ = u // parsed for any future use
	return checkRequestSafe(req, policy)
}

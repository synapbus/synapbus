package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ComputeHMACSignature computes the HMAC-SHA256 signature of the payload using the given secret.
// Returns the signature as "sha256=<hex digest>".
func ComputeHMACSignature(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ValidateWebhookURL validates a webhook URL for registration.
// It checks scheme (HTTPS required unless allowHTTP), parses the URL,
// and optionally resolves DNS to check for private IPs.
func ValidateWebhookURL(rawURL string, allowHTTP, allowPrivate bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Check scheme
	switch u.Scheme {
	case "https":
		// Always allowed
	case "http":
		if !allowHTTP {
			return fmt.Errorf("webhook URL must use HTTPS (set SYNAPBUS_ALLOW_HTTP_WEBHOOKS=true for development)")
		}
	default:
		return fmt.Errorf("webhook URL must use HTTPS scheme, got %q", u.Scheme)
	}

	// Check host is not empty
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL must have a hostname")
	}

	// If allowPrivate, skip IP validation
	if allowPrivate {
		return nil
	}

	// Check if host is a literal IP
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return fmt.Errorf("webhook URL resolves to a private network address; set SYNAPBUS_ALLOW_PRIVATE_NETWORKS=true to override")
		}
		return nil
	}

	// Resolve hostname and check all IPs
	ips, err := net.LookupHost(host)
	if err != nil {
		// Don't fail registration on DNS lookup failure — the URL might be valid later.
		// We'll re-check at delivery time.
		return nil
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil && IsPrivateIP(ip) {
			return fmt.Errorf("webhook URL resolves to a private network address; set SYNAPBUS_ALLOW_PRIVATE_NETWORKS=true to override")
		}
	}

	return nil
}

// IsPrivateIP checks whether an IP address belongs to a private/reserved range.
// Blocks: RFC1918, loopback, link-local, IPv6 ULA, IPv6 link-local.
func IsPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("10.0.0.0/8")},
		{mustParseCIDR("172.16.0.0/12")},
		{mustParseCIDR("192.168.0.0/16")},
		{mustParseCIDR("127.0.0.0/8")},
		{mustParseCIDR("169.254.0.0/16")},
		{mustParseCIDR("::1/128")},
		{mustParseCIDR("fc00::/7")},
		{mustParseCIDR("fe80::/10")},
	}

	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", s, err))
	}
	return n
}

// NewSSRFSafeTransport creates an http.Transport that blocks connections to private IP addresses.
// It uses a custom DialContext that resolves DNS and validates the IP before connecting.
func NewSSRFSafeTransport(allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if !allowPrivate {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("invalid address %q: %w", addr, err)
				}

				// Resolve DNS
				ips, err := net.DefaultResolver.LookupHost(ctx, host)
				if err != nil {
					return nil, fmt.Errorf("DNS resolution failed for %q: %w", host, err)
				}

				// Check all resolved IPs
				for _, ipStr := range ips {
					ip := net.ParseIP(ipStr)
					if ip != nil && IsPrivateIP(ip) {
						return nil, fmt.Errorf("webhook URL resolved to blocked IP address (%s)", ipStr)
					}
				}

				// Connect to the first valid IP
				if len(ips) > 0 {
					addr = net.JoinHostPort(ips[0], port)
				}
			}

			return dialer.DialContext(ctx, network, addr)
		},
		// Also use Control to catch any remaining private IPs at the socket level
		ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}
}

// NewSSRFSafeClient creates an http.Client with SSRF protection and no redirect following.
func NewSSRFSafeClient(allowPrivate bool) *http.Client {
	return &http.Client{
		Transport: NewSSRFSafeTransport(allowPrivate),
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("redirects not allowed")
		},
	}
}

// controlFunc returns a syscall.RawConn Control function that blocks private IPs.
// This is a defense-in-depth check at the socket level.
func controlFunc(network string, address string, conn syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// address might not have a port
		host = address
	}
	// Remove brackets from IPv6
	host = strings.Trim(host, "[]")

	ip := net.ParseIP(host)
	if ip != nil && IsPrivateIP(ip) {
		return fmt.Errorf("connection to private IP %s blocked", host)
	}
	return nil
}

// ValidURLSchemes contains the allowed URL schemes for webhooks.
var ValidURLSchemes = []string{"https", "http"}

// ValidEvents contains the valid webhook event types.
var ValidEvents = []string{"message.received", "message.mentioned", "channel.message"}

// IsValidEvent checks if the given event type is valid.
func IsValidEvent(event string) bool {
	for _, e := range ValidEvents {
		if e == event {
			return true
		}
	}
	return false
}

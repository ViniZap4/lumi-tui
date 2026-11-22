package account

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NormalizeServerURL canonicalises a server base URL per SPEC-LIVROS §2.1:
// lowercase scheme and host, no default port, no trailing slash, no query
// or fragment. A base path (`https://x.com/lumi`) is kept. The result is
// the identity used to key accounts and books, so two spellings of the
// same server collapse to one row.
//
// Normalisation never fails on a merely odd URL — only on one that is not
// an http(s) URL with a host.
func NormalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("server URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("server URL must use http or https, got %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("server URL must include a host")
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	hostport := host
	if strings.Contains(host, ":") { // IPv6 literal
		hostport = "[" + host + "]"
	}
	if port != "" {
		hostport = net.JoinHostPort(host, port)
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	return scheme + "://" + hostport + path, nil
}

// normalizeOrSelf returns the normalised URL, or the trimmed input when it
// cannot be normalised. Used on load paths that must never drop a row just
// because a hand-edited URL is malformed.
func normalizeOrSelf(raw string) string {
	if n, err := NormalizeServerURL(raw); err == nil {
		return n
	}
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// SameServer reports whether two server URLs normalise to the same value.
func SameServer(a, b string) bool {
	return normalizeOrSelf(a) == normalizeOrSelf(b)
}

// IsInsecureAllowed reports whether a plain-http server URL is acceptable
// under SPEC-LIVROS §2.1: only localhost, private IPs and `*.local`.
// https URLs always return true.
func IsInsecureAllowed(serverURL string) bool {
	u, err := url.Parse(serverURL)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// Package webfetch retrieves web pages for the agent: URL vetting, HTTP
// transport with SSRF guards, markdown conversion, caching, and prompt-focused
// report rendering.
package webfetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

const maxURLLength = 2000

// NormalizeURL vets a URL before it is fetched: absolute http(s) only, no
// embedded credentials, and a hostname that does not resolve to a private or
// local address. Plain http is upgraded to https.
func NormalizeURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("web_fetch requires url")
	}
	if len(rawURL) > maxURLLength {
		return "", fmt.Errorf("web_fetch url exceeds %d characters", maxURLLength)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", rawURL, err)
	}
	if parsed.Scheme == "" {
		return "", fmt.Errorf("web_fetch requires an absolute url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("web_fetch only supports http and https urls")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("web_fetch does not allow credentials in urls")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("web_fetch requires a hostname")
	}
	if err := ValidateHost(parsed.Hostname()); err != nil {
		return "", err
	}
	parsed.Scheme = "https"
	return parsed.String(), nil
}

// ValidateHost rejects hostnames that are, or resolve to, addresses on the
// local machine or a private network.
func ValidateHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("web_fetch requires a hostname")
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddr(ip) {
			return errPrivateAddress
		}
		return nil
	}

	if strings.EqualFold(host, "localhost") || !strings.Contains(host, ".") {
		return fmt.Errorf("web_fetch requires a public hostname")
	}

	addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("web_fetch could not resolve %q", host)
	}
	for _, addr := range addrs {
		if !isPublicAddr(addr) {
			return errPrivateAddress
		}
	}
	return nil
}

var errPrivateAddress = fmt.Errorf("web_fetch blocks private or local addresses")

func isPublicAddr(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsMulticast() &&
		!ip.IsInterfaceLocalMulticast()
}

// guardedDialer re-checks the address the resolver actually returned, at the
// moment the socket is opened. Hostname validation alone can be defeated by a
// DNS record that resolves to a public address once and to 127.0.0.1 or a
// metadata endpoint on the second lookup.
func guardedDialer() *net.Dialer {
	dialer := &net.Dialer{}
	dialer.Control = func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("parse dial address %q: %w", address, err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("parse dial address %q: %w", host, err)
		}
		if !isPublicAddr(ip) {
			return errPrivateAddress
		}
		return nil
	}
	return dialer
}

// isRedirectStatus covers every status that carries a Location header a GET
// should follow, including 303, which servers use after form posts.
func isRedirectStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func resolveRedirect(baseURL, location string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse redirect base url: %w", err)
	}
	redirectURL, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse redirect url: %w", err)
	}
	return base.ResolveReference(redirectURL).String(), nil
}

// permittedRedirect keeps a fetch on the site it started on. Following a
// redirect to another host would let a page pull in content the caller never
// asked for.
func permittedRedirect(originalURL, redirectURL string) bool {
	original, err := url.Parse(originalURL)
	if err != nil {
		return false
	}
	redirected, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}
	if redirected.Scheme != original.Scheme || redirected.Port() != original.Port() {
		return false
	}
	if redirected.User != nil {
		return false
	}
	return stripWww(original.Hostname()) == stripWww(redirected.Hostname())
}

func stripWww(host string) string {
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

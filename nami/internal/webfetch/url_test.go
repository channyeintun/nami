package webfetch

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNormalizeURLUpgradesAndAccepts(t *testing.T) {
	cases := map[string]string{
		"https://example.com/docs": "https://example.com/docs",
		"http://example.com/docs":  "https://example.com/docs",
		"  https://example.com  ":  "https://example.com",
		"https://example.com?q=1":  "https://example.com?q=1",
	}
	for input, want := range cases {
		got, err := NormalizeURL(input)
		if err != nil {
			t.Errorf("NormalizeURL(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeURLRejectsUnsafeInput(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"blank":              "   ",
		"relative":           "example.com/docs",
		"unsupported scheme": "ftp://example.com",
		"file scheme":        "file:///etc/passwd",
		"credentials":        "https://user:pass@example.com",
		"no hostname":        "https:///path",
		"loopback ip":        "https://127.0.0.1/admin",
		"ipv6 loopback":      "https://[::1]/admin",
		"private ip":         "https://10.0.0.5/",
		"link local":         "https://169.254.169.254/latest/meta-data",
		"localhost":          "https://localhost:8080/",
		"bare host":          "https://intranet/",
		"too long":           "https://example.com/" + strings.Repeat("a", maxURLLength),
	}
	for name, rawURL := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := NormalizeURL(rawURL); err == nil {
				t.Fatalf("NormalizeURL(%q) = %q, want an error", rawURL, got)
			}
		})
	}
}

func TestIsPublicAddr(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"::1",
		"10.1.2.3",
		"192.168.0.1",
		"172.16.0.1",
		"169.254.169.254",
		"fe80::1",
		"fd00::1",
		"224.0.0.1",
		"ff02::1",
		"0.0.0.0",
		"::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
	}
	for _, raw := range blocked {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if isPublicAddr(addr) {
			t.Errorf("isPublicAddr(%q) = true, want false", raw)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"}
	for _, raw := range allowed {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if !isPublicAddr(addr) {
			t.Errorf("isPublicAddr(%q) = false, want true", raw)
		}
	}
}

// The dial-time guard is what stops a hostname that passes validation from
// connecting to a private address on a second DNS lookup.
func TestGuardedDialerRejectsPrivateTargets(t *testing.T) {
	control := guardedDialer().Control
	if control == nil {
		t.Fatal("guardedDialer has no Control hook")
	}
	for _, address := range []string{"127.0.0.1:80", "[::1]:443", "10.0.0.9:8080", "169.254.169.254:80"} {
		if err := control("tcp", address, nil); err == nil {
			t.Errorf("dial guard allowed %q", address)
		}
	}
	if err := control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("dial guard blocked a public address: %v", err)
	}
	if err := control("tcp", "not-an-address", nil); err == nil {
		t.Error("dial guard accepted a malformed address")
	}
}

func TestValidateHostRejectsBlankAndLocal(t *testing.T) {
	for _, host := range []string{"", "   ", "localhost", "LOCALHOST", "singleword", "127.0.0.1"} {
		if err := ValidateHost(host); err == nil {
			t.Errorf("ValidateHost(%q) = nil, want an error", host)
		}
	}
}

func TestPermittedRedirect(t *testing.T) {
	cases := []struct {
		name   string
		from   string
		to     string
		expect bool
	}{
		{"same host", "https://example.com/a", "https://example.com/b", true},
		{"www prefix added", "https://example.com/a", "https://www.example.com/b", true},
		{"www prefix removed", "https://www.example.com/a", "https://example.com/b", true},
		{"other host", "https://example.com/a", "https://evil.com/b", false},
		{"subdomain", "https://example.com/a", "https://api.example.com/b", false},
		{"scheme downgrade", "https://example.com/a", "http://example.com/b", false},
		{"port change", "https://example.com/a", "https://example.com:8443/b", false},
		{"credentials added", "https://example.com/a", "https://user:pw@example.com/b", false},
		{"unparsable target", "https://example.com/a", "://", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := permittedRedirect(tc.from, tc.to); got != tc.expect {
				t.Fatalf("permittedRedirect(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.expect)
			}
		})
	}
}

func TestResolveRedirect(t *testing.T) {
	got, err := resolveRedirect("https://example.com/docs/page", "../other")
	if err != nil {
		t.Fatalf("resolveRedirect: %v", err)
	}
	if want := "https://example.com/other"; got != want {
		t.Fatalf("resolveRedirect = %q, want %q", got, want)
	}
	if _, err := resolveRedirect("://bad", "/x"); err == nil {
		t.Fatal("resolveRedirect accepted a malformed base url")
	}
}

func TestIsRedirectStatus(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		if !isRedirectStatus(status) {
			t.Errorf("isRedirectStatus(%d) = false, want true", status)
		}
	}
	for _, status := range []int{200, 204, 304, 400, 500} {
		if isRedirectStatus(status) {
			t.Errorf("isRedirectStatus(%d) = true, want false", status)
		}
	}
}

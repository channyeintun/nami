package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/session"
)

// Device-flow verification URLs come from a remote server's reply. The
// platform openers also launch local files and other schemes, and read a
// leading "-" as a flag, so anything but a web URL must be refused before any
// process starts.
func TestOpenBrowserURLRefusesNonWebURLs(t *testing.T) {
	for _, target := range []string{
		"",
		"file:///etc/passwd",
		`C:\Windows\System32\calc.exe`,
		"--help",
		"javascript:alert(1)",
		"https://",
		"mailto:someone@example.com",
	} {
		if err := OpenBrowserURL(target); err == nil {
			t.Errorf("OpenBrowserURL(%q) was accepted", target)
		}
	}
}

func TestValidateBrowserURLAcceptsWebURLs(t *testing.T) {
	for _, target := range []string{"https://github.com/login/device", "http://localhost:1455/auth/callback"} {
		if err := validateBrowserURL(target); err != nil {
			t.Errorf("validateBrowserURL(%q) = %v", target, err)
		}
	}
}

func TestFormatSessionListShortensIDs(t *testing.T) {
	sessions := []session.Metadata{{
		SessionID: "0f8c2d1e-aaaa-bbbb-cccc-000000000000",
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC),
		Model:     "anthropic/claude-sonnet-5",
		Title:     "Fix the build",
	}}
	listing := FormatSessionList(sessions, "0f8c2d1e-aaaa-bbbb-cccc-000000000000")
	if !strings.Contains(listing, "* 0f8c2d1e  ") || strings.Contains(listing, "aaaa") {
		t.Fatalf("listing = %q, want the current session marked with its 8-character id", listing)
	}
}

// The list is built from whatever metadata.json files are on disk. One with a
// short or missing session id must not panic the /sessions command.
func TestFormatSessionListToleratesShortIDs(t *testing.T) {
	sessions := []session.Metadata{
		{SessionID: "abc", Title: "short"},
		{SessionID: "", Title: "missing"},
	}
	listing := FormatSessionList(sessions, "")
	for _, want := range []string{"abc", "short", "missing"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("listing %q is missing %q", listing, want)
		}
	}
}

package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The attempt log records failed commands and their errors, which can carry
// a secret the conversation did, so it must be as private as the rest of the
// session directory.
func TestAttemptLogIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	sessionDir := filepath.Join(t.TempDir(), "session")
	log := NewAttemptLog(sessionDir)
	entry := AttemptEntry{Command: "curl -H 'Authorization: Bearer sk-secret' https://example.com", ErrorSignature: "401 Unauthorized"}
	if err := log.Record(entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	for path, want := range map[string]os.FileMode{
		sessionDir: 0o700,
		filepath.Join(sessionDir, attemptLogFilename): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %o, want %o", filepath.Base(path), got, want)
		}
	}
}

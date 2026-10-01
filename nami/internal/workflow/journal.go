package workflow

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// journalRecord is one line of a run journal: an agent() call's key and the
// result it produced.
type journalRecord struct {
	Key            string          `json:"key"`
	Label          string          `json:"label,omitempty"`
	Text           string          `json:"text,omitempty"`
	Structured     json.RawMessage `json:"structured,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
}

// Journal makes a run resumable. Every successful agent() call is recorded
// under a key built from what the call asked for, and a later run seeded with
// the journal replays a call whose key matches instead of running it again.
//
// A nil *Journal is usable and does nothing, so callers need no nil checks.
type Journal struct {
	mu     sync.Mutex
	path   string
	cached map[string]journalRecord
	file   *os.File
	writer *bufio.Writer
}

// OpenJournal opens the journal at path for appending. When resumeFrom names
// an earlier run's journal, its records seed the replay cache; a missing one
// is a cold start.
//
// A journal already at path is kept, never truncated, so a run can never
// destroy records another run may still want to replay.
func OpenJournal(path string, resumeFrom string) (*Journal, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	// Journals hold agents' output, so they are private to the user.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}

	journal := &Journal{path: path, cached: map[string]journalRecord{}}
	if resume := strings.TrimSpace(resumeFrom); resume != "" {
		if err := journal.load(resume); err != nil {
			return nil, err
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := terminateTornLine(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	journal.file = file
	journal.writer = bufio.NewWriter(file)
	return journal, nil
}

// terminateTornLine ends a line that a killed run left half-written, so the
// next record starts on a line of its own instead of fusing with the fragment.
func terminateTornLine(file *os.File) error {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = file.Write([]byte{'\n'})
	return err
}

func (j *Journal) load(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxJournalLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record journalRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// A half-written line is the normal shape of a killed run, and a
			// later run may have appended after it. Every record stands on its
			// own, so skip the fragment and keep reading.
			continue
		}
		if !strings.HasPrefix(record.Key, journalKeyVersion) {
			continue
		}
		j.cached[record.Key] = record
	}
	return scanner.Err()
}

// maxJournalLineBytes bounds a record. load stops at a longer line, which
// would fail every resume from the journal, so Record refuses to write one.
const maxJournalLineBytes = 4 * 1024 * 1024

// Replay returns the recorded result for a key.
func (j *Journal) Replay(key string) (journalRecord, bool) {
	if j == nil {
		return journalRecord{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.cached[key]
	return record, ok
}

// Record appends a result. A run replays results by appending them again, so
// a resumed run's journal is complete on its own and can itself be resumed.
func (j *Journal) Record(record journalRecord) error {
	if j == nil {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	if len(encoded) >= maxJournalLineBytes {
		return fmt.Errorf("result of %q is %d bytes, more than a journal line can hold", record.Label, len(encoded))
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.writer == nil {
		return errors.New("journal is closed")
	}
	if _, err := j.writer.Write(encoded); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	if err := j.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	// Flushed per record so a run that is killed keeps everything it finished.
	if err := j.writer.Flush(); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	return nil
}

// Path is where the journal is written.
func (j *Journal) Path() string {
	if j == nil {
		return ""
	}
	return j.path
}

// Close flushes and releases the journal file.
func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	flushErr := j.writer.Flush()
	closeErr := j.file.Close()
	j.file = nil
	j.writer = nil
	return errors.Join(flushErr, closeErr)
}

// journalKeyVersion prefixes every key, so a journal written by an older
// keying scheme can never match.
const journalKeyVersion = "v2:"

// callDigest identifies what an agent() call asks for: its prompt and every
// option that changes how the agent runs or what it returns. Label and phase
// are left out, so relabelling a call for a nicer progress display keeps its
// cached result.
//
// A run keys each call as its digest plus how many earlier calls in the run
// had the same digest. Counting per digest, not by a global call index, is
// what keeps keys stable when parallel work finishes in a different order: a
// call's position in the run depends on timing, but its digest and how many
// identical calls came before it do not, and identical calls can swap results
// without anyone noticing.
func callDigest(call AgentCall) string {
	digest := sha256.New()
	for _, part := range []string{call.Prompt, string(call.Schema), call.Model, call.Effort, call.Isolation, call.AgentType} {
		digest.Write([]byte(part))
		digest.Write([]byte{0})
	}
	return journalKeyVersion + hex.EncodeToString(digest.Sum(nil))
}

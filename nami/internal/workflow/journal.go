package workflow

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// journalRecord is one line of a run journal.
type journalRecord struct {
	Key      string            `json:"key"`
	NodeID   string            `json:"node_id"`
	Output   string            `json:"output,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Journal makes a run resumable. Each node's result is recorded under a key
// derived from its dependencies' keys, so a key transitively commits to
// everything the node's result actually depended on. Replaying is therefore
// sound without any global "chain broken" latch: a key can only match when the
// whole ancestry that produced it matched, and a change re-runs exactly the
// affected subgraph rather than everything downstream of it in some order.
//
// A nil *Journal is usable and does nothing, so callers need no nil checks.
type Journal struct {
	mu     sync.Mutex
	path   string
	cached map[string]journalRecord
	file   *os.File
	writer *bufio.Writer
}

// OpenJournal creates (or truncates) the journal for a run. When resumeFrom
// names a readable prior journal, its records seed the replay cache.
func OpenJournal(path string, resumeFrom string) (*Journal, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	journal := &Journal{path: path, cached: map[string]journalRecord{}}
	if resume := strings.TrimSpace(resumeFrom); resume != "" {
		if err := journal.load(resume); err != nil {
			return nil, err
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	journal.file = file
	journal.writer = bufio.NewWriter(file)
	return journal, nil
}

func (j *Journal) load(path string) error {
	file, err := os.Open(path)
	if err != nil {
		// A missing prior journal is a cold start, not a failure: the run
		// proceeds with an empty cache and executes every node.
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
			// A truncated tail is the normal shape of a killed run. Keep the
			// records read so far rather than discarding a usable prefix.
			break
		}
		j.cached[record.Key] = record
	}
	return scanner.Err()
}

const maxJournalLineBytes = 4 * 1024 * 1024

// Replay returns a recorded result for the key, if the chain is still intact.
func (j *Journal) Replay(key string) (NodeResult, bool) {
	if j == nil {
		return NodeResult{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.cached[key]
	if !ok {
		return NodeResult{}, false
	}
	// A replayed node is still journaled, so the resumed run's journal is a
	// complete record on its own and can itself be resumed from.
	j.append(record)
	return NodeResult{Output: record.Output, Metadata: record.Metadata}, true
}

// Record appends a node result.
func (j *Journal) Record(key string, nodeID string, result NodeResult) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.append(journalRecord{
		Key:      key,
		NodeID:   nodeID,
		Output:   result.Output,
		Metadata: result.Metadata,
	})
}

// append writes one record. The caller holds the lock.
func (j *Journal) append(record journalRecord) {
	if j.writer == nil {
		return
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	// Journaling is best-effort: a run that cannot record its progress should
	// still produce its results, it just will not be resumable.
	_, _ = j.writer.Write(encoded)
	_ = j.writer.WriteByte('\n')
	_ = j.writer.Flush()
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
	if j == nil || j.file == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.writer.Flush(); err != nil {
		_ = j.file.Close()
		j.file = nil
		return err
	}
	err := j.file.Close()
	j.file = nil
	j.writer = nil
	return err
}

// nodeKey derives a node's journal key from its dependencies' keys plus its own
// executable identity: the expanded prompt and the settings that change how it
// runs. Description is deliberately excluded, so relabeling a node for a nicer
// progress display does not invalidate its cached result.
//
// Keying on dependency keys rather than on a linear "everything before this"
// chain is what makes resume work for a graph that runs in parallel. Launch
// order varies between runs once more than one node is in flight, so a linear
// chain would produce different keys for the same work and miss on every
// resume. Dependency keys depend only on the graph and the data flowing
// through it.
//
// The expanded prompt is what closes the loop on upstream results: if a
// dependency re-ran and produced a different output, any node that interpolates
// that output has a different prompt and so a different key. A node that does
// not interpolate it was genuinely unaffected, and replaying it is correct.
func nodeKey(dependencyKeys []string, node ResolvedNode, prompt string) string {
	digest := sha256.New()
	// Sorted so the key does not depend on the order dependencies were declared.
	ordered := slices.Sorted(slices.Values(dependencyKeys))
	for _, key := range ordered {
		digest.Write([]byte(key))
		digest.Write([]byte{0})
	}
	digest.Write([]byte(node.ID))
	digest.Write([]byte{0})
	digest.Write([]byte(prompt))
	digest.Write([]byte{0})
	digest.Write([]byte(node.Agent.SubagentType))
	digest.Write([]byte{0})
	digest.Write([]byte(node.Agent.Role))
	digest.Write([]byte{0})
	digest.Write([]byte(node.Agent.WorkspaceStrategy))
	return "v1:" + hex.EncodeToString(digest.Sum(nil))
}

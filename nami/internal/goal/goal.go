// Package goal implements a session-scoped stop condition: a goal the agent
// must satisfy before its turn is allowed to end. Setting one turns a single
// request into a loop — the agent keeps working, and each time it tries to
// stop the condition is checked against what actually happened.
package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	stateFilename = "goal.json"
	// MaxConditionChars bounds the condition. A condition long enough to exceed
	// this is a task description, and the evaluator judges those poorly.
	MaxConditionChars = 4000
	// DefaultBlockCap is how many times in a row the goal may block completion
	// before the loop gives up and hands control back. Without a cap, a goal the
	// agent cannot satisfy traps the session.
	DefaultBlockCap = 8
)

// State is one session's goal.
type State struct {
	Condition string    `json:"condition"`
	SetAt     time.Time `json:"set_at"`
	// Iterations counts how many times the goal was evaluated.
	Iterations int `json:"iterations"`
	// ConsecutiveBlocks counts blocks since the last time the agent made
	// visible progress. It resets on progress, so a long but productive loop
	// never trips the cap.
	ConsecutiveBlocks int    `json:"consecutive_blocks"`
	LastReason        string `json:"last_reason,omitempty"`
}

// Store holds the active goal and mirrors it to the session directory so it
// survives a reconnect. The session directory is already where per-session
// state lives; the goal expires with it.
//
// A nil *Store behaves as if no goal is set, so callers need no nil checks.
type Store struct {
	mu         sync.RWMutex
	sessionDir string
	state      *State
	clock      func() time.Time
}

// NewStore creates a store rooted at a session directory. An empty directory
// keeps the goal in memory only.
func NewStore(sessionDir string) *Store {
	return &Store{sessionDir: strings.TrimSpace(sessionDir), clock: time.Now}
}

// ErrConditionTooLong reports a condition past MaxConditionChars.
var ErrConditionTooLong = errors.New("goal condition is too long")

// ErrConditionEmpty reports a blank condition.
var ErrConditionEmpty = errors.New("goal condition is empty")

// Set replaces the active goal and returns the one it displaced, if any.
func (s *Store) Set(condition string) (previous string, err error) {
	if s == nil {
		return "", nil
	}
	condition = strings.TrimSpace(condition)
	switch {
	case condition == "":
		return "", ErrConditionEmpty
	case len([]rune(condition)) > MaxConditionChars:
		return "", fmt.Errorf("%w: %d characters, limit is %d", ErrConditionTooLong, len([]rune(condition)), MaxConditionChars)
	}

	s.mu.Lock()
	if s.state != nil {
		previous = s.state.Condition
	}
	s.state = &State{Condition: condition, SetAt: s.now()}
	snapshot := *s.state
	s.mu.Unlock()

	s.persist(&snapshot)
	return previous, nil
}

// Clear removes the active goal and returns what it was.
func (s *Store) Clear() (previous string) {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	if s.state != nil {
		previous = s.state.Condition
	}
	s.state = nil
	s.mu.Unlock()

	s.persist(nil)
	return previous
}

// Snapshot returns the active goal, if there is one.
func (s *Store) Snapshot() (State, bool) {
	if s == nil {
		return State{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == nil {
		return State{}, false
	}
	return *s.state, true
}

// Active reports whether a goal is set.
func (s *Store) Active() bool {
	_, ok := s.Snapshot()
	return ok
}

// NoteEvaluated records that the goal was checked, without judging the outcome.
func (s *Store) NoteEvaluated() {
	s.mutate(func(state *State) { state.Iterations++ })
}

// NoteBlocked records that the goal blocked completion.
func (s *Store) NoteBlocked(reason string) {
	s.mutate(func(state *State) {
		state.ConsecutiveBlocks++
		state.LastReason = strings.TrimSpace(reason)
	})
}

// NoteProgress resets the consecutive-block counter. The cap exists to catch a
// loop that is spinning, not one that is simply long, so any real work the
// agent does between blocks buys the loop a fresh budget.
func (s *Store) NoteProgress() {
	s.mutate(func(state *State) { state.ConsecutiveBlocks = 0 })
}

func (s *Store) mutate(apply func(*State)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.state == nil {
		s.mu.Unlock()
		return
	}
	apply(s.state)
	snapshot := *s.state
	s.mu.Unlock()

	s.persist(&snapshot)
}

func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// Load restores a goal persisted by an earlier run of this session. A missing
// or unreadable file simply means no goal.
func (s *Store) Load() {
	if s == nil || s.sessionDir == "" {
		return
	}
	data, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if strings.TrimSpace(state.Condition) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = &state
}

func (s *Store) path() string {
	return filepath.Join(s.sessionDir, stateFilename)
}

// persist mirrors the state to disk. Failures are silent: losing the mirror
// costs the goal its survival across a reconnect, and refusing to run the loop
// over that would be a worse trade.
func (s *Store) persist(state *State) {
	if s.sessionDir == "" {
		return
	}
	path := s.path()
	if state == nil {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(s.sessionDir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	// Write through a temporary file so a crash mid-write cannot leave a
	// half-written goal that reads back as a different condition.
	temp, err := os.CreateTemp(s.sessionDir, stateFilename+".*")
	if err != nil {
		return
	}
	tempPath := temp.Name()
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
	}
}

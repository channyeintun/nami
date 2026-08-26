package goal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetAndSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	if store.Active() {
		t.Fatal("a fresh store reports an active goal")
	}

	previous, err := store.Set("  all tests pass  ")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if previous != "" {
		t.Fatalf("previous = %q, want empty", previous)
	}

	state, ok := store.Snapshot()
	if !ok {
		t.Fatal("no goal after Set")
	}
	if state.Condition != "all tests pass" {
		t.Fatalf("Condition = %q", state.Condition)
	}
	if state.SetAt.IsZero() {
		t.Fatal("SetAt was not stamped")
	}
}

func TestSetReturnsTheDisplacedGoal(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Set("first"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	previous, err := store.Set("second")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if previous != "first" {
		t.Fatalf("previous = %q, want first", previous)
	}
	state, _ := store.Snapshot()
	// Replacing a goal must reset the counters; carrying them over would let a
	// new goal inherit an almost-exhausted block budget.
	if state.ConsecutiveBlocks != 0 || state.Iterations != 0 {
		t.Fatalf("counters carried over: %+v", state)
	}
}

func TestSetRejectsEmptyAndOverlongConditions(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Set("   "); !errors.Is(err, ErrConditionEmpty) {
		t.Fatalf("err = %v, want ErrConditionEmpty", err)
	}
	if _, err := store.Set(strings.Repeat("x", MaxConditionChars+1)); !errors.Is(err, ErrConditionTooLong) {
		t.Fatalf("err = %v, want ErrConditionTooLong", err)
	}
	if store.Active() {
		t.Fatal("a rejected condition still set a goal")
	}
}

func TestClearReturnsThePreviousGoal(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Set("ship it"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if previous := store.Clear(); previous != "ship it" {
		t.Fatalf("Clear = %q", previous)
	}
	if store.Active() {
		t.Fatal("goal survived Clear")
	}
	if previous := store.Clear(); previous != "" {
		t.Fatalf("second Clear = %q, want empty", previous)
	}
}

func TestBlockAndProgressCounters(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Set("done"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	store.NoteEvaluated()
	store.NoteBlocked("tests still failing")
	store.NoteEvaluated()
	store.NoteBlocked("still failing")

	state, _ := store.Snapshot()
	if state.Iterations != 2 || state.ConsecutiveBlocks != 2 {
		t.Fatalf("state = %+v", state)
	}
	if state.LastReason != "still failing" {
		t.Fatalf("LastReason = %q", state.LastReason)
	}

	// Progress resets only the consecutive-block budget, never the total count
	// of evaluations, which is what the status line reports.
	store.NoteProgress()
	state, _ = store.Snapshot()
	if state.ConsecutiveBlocks != 0 {
		t.Fatalf("ConsecutiveBlocks = %d after progress", state.ConsecutiveBlocks)
	}
	if state.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2", state.Iterations)
	}
}

func TestCountersOnAClearedStoreAreNoOps(t *testing.T) {
	store := NewStore(t.TempDir())
	store.NoteEvaluated()
	store.NoteBlocked("x")
	store.NoteProgress()
	if store.Active() {
		t.Fatal("counters resurrected a cleared goal")
	}
}

func TestNilStoreIsUsable(t *testing.T) {
	var store *Store
	if store.Active() {
		t.Fatal("nil store is active")
	}
	if _, ok := store.Snapshot(); ok {
		t.Fatal("nil store has a snapshot")
	}
	if previous, err := store.Set("x"); previous != "" || err != nil {
		t.Fatalf("Set on nil store = %q, %v", previous, err)
	}
	if previous := store.Clear(); previous != "" {
		t.Fatalf("Clear on nil store = %q", previous)
	}
	store.NoteEvaluated()
	store.NoteBlocked("x")
	store.NoteProgress()
	store.Load()
}

func TestGoalPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if _, err := store.Set("green build"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	store.NoteEvaluated()
	store.NoteBlocked("still red")

	data, err := os.ReadFile(filepath.Join(dir, stateFilename))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var persisted State
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if persisted.Condition != "green build" || persisted.ConsecutiveBlocks != 1 {
		t.Fatalf("persisted = %+v", persisted)
	}

	reloaded := NewStore(dir)
	reloaded.Load()
	state, ok := reloaded.Snapshot()
	if !ok {
		t.Fatal("goal did not reload")
	}
	if state.Condition != "green build" || state.LastReason != "still red" {
		t.Fatalf("reloaded = %+v", state)
	}
}

func TestClearRemovesThePersistedState(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if _, err := store.Set("x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	store.Clear()

	if _, err := os.Stat(filepath.Join(dir, stateFilename)); !os.IsNotExist(err) {
		t.Fatalf("state file survived Clear: %v", err)
	}
	reloaded := NewStore(dir)
	reloaded.Load()
	if reloaded.Active() {
		t.Fatal("cleared goal reloaded")
	}
}

func TestLoadIgnoresCorruptState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFilename), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := NewStore(dir)
	store.Load()
	if store.Active() {
		t.Fatal("corrupt state produced an active goal")
	}
}

func TestStoreWithoutASessionDirStaysInMemory(t *testing.T) {
	store := NewStore("")
	if _, err := store.Set("x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !store.Active() {
		t.Fatal("in-memory goal was not set")
	}
	store.Load()
	if state, _ := store.Snapshot(); state.Condition != "x" {
		t.Fatalf("Load clobbered the in-memory goal: %+v", state)
	}
}

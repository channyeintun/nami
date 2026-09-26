//go:build unix

package agent

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRetrievalSkipsFIFOsNamedInTheTurn(t *testing.T) {
	// Opening a FIFO blocks until a writer appears, so a pipe that merely
	// has a source-like name must never be treated as a candidate file.
	dir := t.TempDir()
	fifo := filepath.Join(dir, "events.json")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	done := make(chan []LiveSnippet, 1)
	go func() {
		graph := NewRetrievalGraph(dir)
		anchors := ExtractAnchors("the parser reads events.json", "", "", graph)
		candidates, _ := ScoreCandidates(anchors, dir, "", nil, graph)
		done <- ReadLiveSnippets(candidates, retrievalMaxTotalTokens)
	}()

	select {
	case snippets := <-done:
		if len(snippets) != 0 {
			t.Fatalf("expected no snippets from a FIFO, got %+v", snippets)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live retrieval blocked opening a FIFO named in the prompt")
	}
}

func TestMemoryFilesSkipFIFOs(t *testing.T) {
	// Instruction files are read from every directory above the working
	// directory, so a pipe named AGENTS.md must not stall the session.
	dir := t.TempDir()
	for _, name := range []string{"AGENTS.md", "MEMORY.md"} {
		if err := syscall.Mkfifo(filepath.Join(dir, name), 0o644); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
	}

	done := make(chan error, 2)
	go func() {
		_, err := readMemoryFile(filepath.Join(dir, "AGENTS.md"))
		done <- err
		_, err = readMemoryIndex(filepath.Join(dir, "MEMORY.md"))
		done <- err
	}()

	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("expected reading a FIFO as a memory file to fail")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reading a memory file blocked on a FIFO")
		}
	}
}

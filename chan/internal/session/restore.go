package session

import (
	"fmt"

	"github.com/channyeintun/gocode/internal/api"
)

// RestoredState is the persisted session state needed to resume a conversation.
type RestoredState struct {
	Metadata Metadata
	Messages []api.Message
}

// Restore loads metadata and transcript for a session.
func (s *Store) Restore(sessionID string) (RestoredState, error) {
	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		return RestoredState{}, err
	}

	messages, err := s.LoadTranscript(sessionID)
	if err != nil {
		return RestoredState{}, err
	}

	return RestoredState{Metadata: meta, Messages: messages}, nil
}

// LatestResumeCandidate returns the most recent session other than the current one.
func (s *Store) LatestResumeCandidate(currentSessionID string) (Metadata, error) {
	sessions, err := s.ListSessions()
	if err != nil {
		return Metadata{}, err
	}

	for _, meta := range sessions {
		if meta.SessionID == "" || meta.SessionID == currentSessionID {
			continue
		}
		return meta, nil
	}

	return Metadata{}, fmt.Errorf("no resumable sessions found")
}

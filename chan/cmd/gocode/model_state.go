package main

import (
	"sync"

	"github.com/channyeintun/gocode/internal/api"
)

type activeModelState struct {
	mu      sync.RWMutex
	client  api.LLMClient
	modelID string
}

func newActiveModelState(client api.LLMClient, modelID string) *activeModelState {
	return &activeModelState{client: client, modelID: modelID}
}

func (s *activeModelState) Get() (api.LLMClient, string) {
	if s == nil {
		return nil, ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client, s.modelID
}

func (s *activeModelState) Set(client api.LLMClient, modelID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = client
	s.modelID = modelID
}

package localmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// LocalModel interfaces with a local model via Ollama.
type LocalModel struct {
	BaseURL   string
	ModelName string
	client    *http.Client
}

// NewLocalModel creates a local model client.
func NewLocalModel(baseURL, modelName string) *LocalModel {
	return &LocalModel{
		BaseURL:   baseURL,
		ModelName: modelName,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Defaults for local model.
const (
	DefaultOllamaURL   = "http://localhost:11434"
	DefaultLocalModel   = "gemma4-e4b"
)

// DetectLocalModel checks if ollama is running and has a suitable model.
func DetectLocalModel() (*LocalModel, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(DefaultOllamaURL + "/api/tags")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, false
	}

	// Look for preferred local models
	preferred := []string{DefaultLocalModel, "gemma3", "llama3", "qwen2"}
	for _, pref := range preferred {
		for _, m := range result.Models {
			if m.Name == pref || len(m.Name) > len(pref) && m.Name[:len(pref)] == pref {
				return NewLocalModel(DefaultOllamaURL, m.Name), true
			}
		}
	}

	// Use any available model
	if len(result.Models) > 0 {
		return NewLocalModel(DefaultOllamaURL, result.Models[0].Name), true
	}

	return nil, false
}

// Query sends a prompt to the local model and returns the response.
func (m *LocalModel) Query(prompt string, maxTokens int) (string, error) {
	_ = maxTokens // TODO: use in options
	_ = prompt
	return "", fmt.Errorf("local model query not yet implemented")
}

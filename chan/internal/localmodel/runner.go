package localmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	DefaultOllamaURL  = "http://localhost:11434"
	DefaultLocalModel = "gemma4-e4b"
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
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("local model prompt is empty")
	}

	request := struct {
		Model   string         `json:"model"`
		Prompt  string         `json:"prompt"`
		Stream  bool           `json:"stream"`
		Options map[string]any `json:"options,omitempty"`
	}{
		Model:  m.ModelName,
		Prompt: prompt,
		Stream: false,
	}
	if maxTokens > 0 {
		request.Options = map[string]any{"num_predict": maxTokens}
	}

	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal ollama request: %w", err)
	}

	endpoint := strings.TrimRight(m.BaseURL, "/") + "/api/generate"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama generate failed: %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var result struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", fmt.Errorf("ollama generate failed: %s", strings.TrimSpace(result.Error))
	}

	response := strings.TrimSpace(result.Response)
	if response == "" {
		return "", fmt.Errorf("ollama returned empty response")
	}
	return response, nil
}

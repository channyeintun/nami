package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RecentModelSelection struct {
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	ExplicitProvider bool   `json:"explicit_provider,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

func RecentModelPath() string {
	return filepath.Join(ConfigDir(), "recent-model.json")
}

func LoadRecentModelSelection() (RecentModelSelection, error) {
	data, err := os.ReadFile(RecentModelPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RecentModelSelection{}, nil
		}
		return RecentModelSelection{}, err
	}

	var recent RecentModelSelection
	if err := json.Unmarshal(data, &recent); err != nil {
		return RecentModelSelection{}, err
	}
	recent.Provider = strings.TrimSpace(recent.Provider)
	recent.Model = strings.TrimSpace(recent.Model)
	if recent.Provider == "" && recent.Model != "" {
		selection := ParseModelSelection(recent.Model, "recent")
		recent.Provider = selection.ProviderID
		recent.Model = selection.ModelID
		recent.ExplicitProvider = selection.ExplicitProvider
	}
	return recent, nil
}

func SaveRecentModelSelection(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	// The same directory holds config.json and its credentials; see Save.
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	selection := ParseModelSelection(model, "recent")
	data, err := json.MarshalIndent(RecentModelSelection{
		Provider:         selection.ProviderID,
		Model:            selection.ModelID,
		ExplicitProvider: selection.ExplicitProvider,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(RecentModelPath(), data, 0o644)
}

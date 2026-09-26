package config

import (
	"fmt"
	"sync"
	"testing"
)

// Saves come from background token refreshes as well as from slash commands.
// Each one reads the config, changes its own fields and writes it back, so two
// that overlap must not let the later write undo the earlier one's change.
// Run with -race.
func TestConcurrentUpdatesKeepEveryChange(t *testing.T) {
	useTempConfigDir(t)
	const writers = 32
	var wg sync.WaitGroup
	for writer := range writers {
		wg.Go(func() {
			err := Update(func(cfg *Config) {
				if cfg.Providers == nil {
					cfg.Providers = make(map[string]ProviderOverride)
				}
				cfg.Providers[fmt.Sprintf("provider-%d", writer)] = ProviderOverride{DefaultModel: "model"}
			})
			if err != nil {
				t.Errorf("Update from writer %d: %v", writer, err)
			}
		})
	}
	wg.Wait()

	if got := len(LoadUser().Providers); got != writers {
		t.Fatalf("config keeps %d of %d writers' changes", got, writers)
	}
}

// Update starts from the config as the user saved it, so an environment
// override stays an override, and it keeps Save's refusal to replace a
// config.json that no longer parses.
func TestUpdateSavesOnlyWhatItChanges(t *testing.T) {
	useTempConfigDir(t)
	t.Setenv("NAMI_PERMISSION_MODE", "bypassPermissions")

	if err := Update(func(cfg *Config) { cfg.ReasoningEffort = "high" }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	saved := LoadUser()
	if saved.ReasoningEffort != "high" || saved.PermissionMode != "" {
		t.Fatalf("saved reasoning %q and permission mode %q, want only the change", saved.ReasoningEffort, saved.PermissionMode)
	}
}

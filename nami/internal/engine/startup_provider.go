package engine

import (
	"fmt"
	"strings"

	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
)

type startupProviderSelection struct {
	Provider string
	Model    string
	Snapshot commandspkg.ProviderSnapshot
	Notice   string
}

func resolveStartupProviderSelection(cfg config.Config) startupProviderSelection {
	effectiveCfg := cfg
	originalProvider, originalModel := commandspkg.ResolveActiveSelection(cfg)
	originalSelection := originalModel
	if originalProvider != "" {
		originalSelection = originalProvider + "/" + originalModel
	}
	startupNotice := ""
	if shouldPreferRecentModel(cfg.ModelSource) {
		if recent, err := config.LoadRecentModelSelection(); err == nil {
			recentModel := config.NewModelSelection(recent.Provider, recent.Model, "recent", recent.ExplicitProvider).Ref()
			if strings.TrimSpace(recent.Model) != "" && !strings.EqualFold(recentModel, originalSelection) {
				recentCfg := cfg
				recentCfg.Provider = recent.Provider
				recentCfg.Model = recent.Model
				if recentCfg.Provider == "" {
					recentCfg.Provider, recentCfg.Model = commandspkg.ResolveModelSelection(recent.Model)
				}
				recentSnapshot := commandspkg.DiscoverProviderSnapshot(recentCfg)
				recentProvider, _ := commandspkg.ResolveActiveSelection(recentCfg)
				if status, ok := recentSnapshot.LookupProvider(recentProvider); ok && status.Usable {
					effectiveCfg.Provider = recentCfg.Provider
					effectiveCfg.Model = recentCfg.Model
					startupNotice = fmt.Sprintf("Using recent successful model %s instead of %s.", recentModel, originalSelection)
				}
			}
		}
	}

	snapshot := commandspkg.DiscoverProviderSnapshot(effectiveCfg)
	provider, model := commandspkg.ResolveActiveSelection(effectiveCfg)

	if provider != "" {
		if status, ok := snapshot.LookupProvider(provider); ok && status.Usable {
			if strings.TrimSpace(model) == "" {
				model = status.DefaultModel
			}
			return startupProviderSelection{
				Provider: provider,
				Model:    model,
				Snapshot: snapshot,
				Notice:   startupNotice,
			}
		}
	}

	if fallback, ok := firstStartupFallbackProvider(snapshot); ok {
		selection := startupProviderSelection{
			Provider: fallback.ID,
			Model:    fallback.DefaultModel,
			Snapshot: snapshot,
			Notice:   startupNotice,
		}
		fallbackRef := modelRef(fallback.ID, fallback.DefaultModel)
		preferredSelection := strings.TrimSpace(effectiveCfg.Model)
		if preferredSelection != "" && !strings.EqualFold(preferredSelection, fallbackRef) {
			selection.Notice = appendStartupNotice(selection.Notice, fmt.Sprintf("Preferred model %s is unavailable; using %s instead. Run /providers for setup details.", preferredSelection, fallbackRef))
		}
		return selection
	}

	selection := startupProviderSelection{
		Provider: provider,
		Model:    model,
		Snapshot: snapshot,
		Notice:   startupNotice,
	}
	if current, ok := snapshot.LookupProvider(provider); ok && !current.Usable {
		selection.Notice = appendStartupNotice(selection.Notice, formatNoUsableProviderNotice(current))
	} else {
		selection.Notice = appendStartupNotice(selection.Notice, fmt.Sprintf("Provider %s is not available. Run /providers for setup guidance.", provider))
	}
	return selection
}

func appendStartupNotice(existing string, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	switch {
	case existing == "":
		return next
	case next == "":
		return existing
	default:
		return existing + " " + next
	}
}

func shouldPreferRecentModel(source string) bool {
	switch strings.TrimSpace(source) {
	case "", "default", "config":
		return true
	default:
		return false
	}
}

func firstStartupFallbackProvider(snapshot commandspkg.ProviderSnapshot) (commandspkg.ProviderStatus, bool) {
	for _, status := range snapshot.Providers {
		if !status.Usable {
			continue
		}
		if status.ID == "ollama" {
			continue
		}
		return status, true
	}
	return commandspkg.ProviderStatus{}, false
}

// formatNoUsableProviderNotice explains that the session's provider is not
// set up. It names that provider rather than claiming none is usable: a local
// runtime such as Ollama counts as usable without being checked, so it is
// never switched to at startup, and /status lists it as the first usable one.
func formatNoUsableProviderNotice(status commandspkg.ProviderStatus) string {
	if strings.TrimSpace(status.SetupHint) == "" {
		return fmt.Sprintf("%s is not set up. Run /providers for setup guidance.", status.ID)
	}
	return fmt.Sprintf("%s is not set up: %s Run /providers for more details.", status.ID, status.SetupHint)
}

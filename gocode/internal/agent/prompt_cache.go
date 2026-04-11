package agent

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/channyeintun/gocode/internal/api"
)

type promptCacheEntry struct {
	key   string
	value string
}

// PromptAssemblyCache memoizes prompt sections within a session so identical
// sections are not rebuilt on every turn.
type PromptAssemblyCache struct {
	base    promptCacheEntry
	skill   promptCacheEntry
	memory  promptCacheEntry
	context promptCacheEntry
	final   promptCacheEntry
}

func NewPromptAssemblyCache() *PromptAssemblyCache {
	return &PromptAssemblyCache{}
}

func (c *PromptAssemblyCache) Compose(basePrompt string, sys SystemContext, turn TurnContext, currentUserPrompt string, recalls []MemoryRecallResult, capabilities api.ModelCapabilities, skillPrompt string) string {
	if c == nil {
		return composeSystemPrompt(basePrompt, sys, turn, currentUserPrompt, recalls, capabilities, skillPrompt)
	}

	basePrompt = c.memoize(&c.base, hashStrings("base", basePrompt), func() string {
		return strings.TrimSpace(basePrompt)
	})
	skillPrompt = c.memoize(&c.skill, hashStrings("skill", skillPrompt), func() string {
		return strings.TrimSpace(skillPrompt)
	})
	memoryPrompt := c.memoize(&c.memory, memoryPromptCacheKey(currentUserPrompt, recalls), func() string {
		return strings.TrimSpace(FormatMemoryPrompt(sys.MemoryFiles, currentUserPrompt, recalls))
	})
	contextPrompt := c.memoize(&c.context, contextPromptCacheKey(turn), func() string {
		return strings.TrimSpace(FormatContextPrompt(sys, turn))
	})

	finalKey := hashStrings("final", boolString(capabilities.SupportsCaching), basePrompt, skillPrompt, memoryPrompt, contextPrompt)
	return c.memoize(&c.final, finalKey, func() string {
		return joinPromptSections(orderedPromptSections(capabilities.SupportsCaching, basePrompt, skillPrompt, memoryPrompt, contextPrompt))
	})
}

func (c *PromptAssemblyCache) memoize(entry *promptCacheEntry, key string, build func() string) string {
	if entry.key == key {
		return entry.value
	}
	entry.key = key
	entry.value = build()
	return entry.value
}

func memoryPromptCacheKey(currentUserPrompt string, recalls []MemoryRecallResult) string {
	parts := []string{"memory", currentUserPrompt}
	for _, recall := range recalls {
		parts = append(parts, recall.Path, recall.Source)
		parts = append(parts, recall.Lines...)
	}
	return hashStrings(parts...)
}

func contextPromptCacheKey(turn TurnContext) string {
	return hashStrings(
		"context",
		turn.CurrentDir,
		turn.GitBranch,
		turn.GitStatus,
		turn.RecentLog,
		turn.DirectoryListing,
	)
}

func hashStrings(parts ...string) string {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum64())
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

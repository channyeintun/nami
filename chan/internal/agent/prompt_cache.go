package agent

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/channyeintun/chan/internal/api"
)

type promptCacheEntry struct {
	key   string
	value string
}

// PromptAssemblyCache memoizes prompt sections within a session so identical
// sections are not rebuilt on every turn.
type PromptAssemblyCache struct {
	base    promptCacheEntry
	memory  promptCacheEntry
	context promptCacheEntry
	final   promptCacheEntry
}

func NewPromptAssemblyCache() *PromptAssemblyCache {
	return &PromptAssemblyCache{}
}

func (c *PromptAssemblyCache) Compose(basePrompt string, sys SystemContext, turn TurnContext, currentUserPrompt string, recalls []MemoryRecallResult, sessionMemory SessionMemorySnapshot, capabilities api.ModelCapabilities, skillPrompt string, liveRetrievalSection string, attemptLogSection string) string {
	if c == nil {
		return composeSystemPrompt(basePrompt, sys, turn, currentUserPrompt, recalls, sessionMemory, capabilities, skillPrompt, liveRetrievalSection, attemptLogSection)
	}

	basePrompt = c.memoize(&c.base, hashStrings("base", basePrompt), func() string {
		return strings.TrimSpace(basePrompt)
	})
	memoryPrompt := c.memoize(&c.memory, memoryInstructionPromptCacheKey(sys.MemoryFiles), func() string {
		return strings.TrimSpace(FormatMemoryInstructionPrompt(sys.MemoryFiles))
	})
	contextPrompt := c.memoize(&c.context, systemContextPromptCacheKey(sys), func() string {
		return strings.TrimSpace(FormatSystemContextPrompt(sys))
	})

	if !capabilities.SupportsCaching {
		return composeLegacySystemPrompt(basePrompt, sys, turn, currentUserPrompt, recalls, sessionMemory, capabilities, skillPrompt, liveRetrievalSection, attemptLogSection)
	}

	finalKey := hashStrings("final", boolString(capabilities.SupportsCaching), basePrompt, memoryPrompt, contextPrompt)
	return c.memoize(&c.final, finalKey, func() string {
		return joinPromptSections([]string{basePrompt, memoryPrompt, contextPrompt})
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

func memoryInstructionPromptCacheKey(files []MemoryFile) string {
	parts := []string{"memory-instructions"}
	for _, file := range files {
		if file.Type == memoryTypeProjectIndex || file.Type == memoryTypeUserIndex {
			continue
		}
		parts = append(parts, file.Path, file.Type, file.Content)
	}
	return hashStrings(parts...)
}

func systemContextPromptCacheKey(sys SystemContext) string {
	return hashStrings(
		"context",
		sys.MainBranch,
		sys.GitUser,
		sys.OS,
		sys.Architecture,
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

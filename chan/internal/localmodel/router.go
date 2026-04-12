package localmodel

import (
	"os"
	"strings"

	"github.com/channyeintun/gocode/internal/api"
)

// TaskType classifies internal tasks for model routing.
type TaskType int

const (
	TaskCompaction    TaskType = iota // prefer local
	TaskScoring                       // prefer local
	TaskTitleGen                      // prefer local
	TaskIntentDetect                  // prefer local
	TaskMainReasoning                 // always remote
)

// Router decides whether to use local or remote model for a task.
type Router struct {
	local      *LocalModel
	remote     api.LLMClient
	localAvail bool
}

// NewRouter creates a model router.
func NewRouter(remote api.LLMClient) *Router {
	r := &Router{remote: remote}
	if !UseLocalModelEnabled() {
		return r
	}
	local, ok := DetectLocalModel()
	if ok {
		r.local = local
		r.localAvail = true
	}
	return r
}

// UseLocalModelEnabled reports whether local-model routing is enabled.
// This is opt-in so helper tasks like compaction do not silently switch to Ollama.
func UseLocalModelEnabled() bool {
	value := strings.TrimSpace(os.Getenv("USE_LOCAL_MODEL"))
	return strings.EqualFold(value, "true") || value == "1"
}

// IsLocalAvailable returns whether a local model is ready.
func (r *Router) IsLocalAvailable() bool {
	return r.localAvail
}

// LocalModelName returns the detected local model name, or empty string.
func (r *Router) LocalModelName() string {
	if r.local == nil {
		return ""
	}
	return r.local.ModelName
}

// ShouldUseLocal returns true if the task should be routed to the local model.
func (r *Router) ShouldUseLocal(task TaskType) bool {
	if !r.localAvail {
		return false
	}
	switch task {
	case TaskCompaction, TaskScoring, TaskTitleGen, TaskIntentDetect:
		return true
	default:
		return false
	}
}

// TryLocal runs a task on the local model when routing allows it.
// The returned bool reports whether a local attempt was made.
func (r *Router) TryLocal(task TaskType, prompt string, maxTokens int) (string, bool, error) {
	if !r.ShouldUseLocal(task) || r.local == nil {
		return "", false, nil
	}

	response, err := r.local.Query(prompt, maxTokens)
	if err != nil {
		return "", true, err
	}
	return response, true, nil
}

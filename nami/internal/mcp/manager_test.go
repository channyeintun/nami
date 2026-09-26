package mcp

import (
	"context"
	"sync/atomic"
	"testing"
)

// fakeSession is a Session whose only observable behavior is whether Close was
// called, so tests can prove the manager owns session cleanup.
type fakeSession struct {
	id     string
	closed atomic.Bool
}

func (f *fakeSession) ID() string                                                  { return f.id }
func (f *fakeSession) ServerInfo() ServerInfo                                      { return ServerInfo{} }
func (f *fakeSession) HasResourcesCapability() bool                                { return false }
func (f *fakeSession) ListTools(context.Context) ([]ToolDescriptor, error)         { return nil, nil }
func (f *fakeSession) ListPrompts(context.Context) ([]PromptDescriptor, error)     { return nil, nil }
func (f *fakeSession) ListResources(context.Context) ([]ResourceDescriptor, error) { return nil, nil }
func (f *fakeSession) ReadResource(context.Context, string) ([]ResourceContent, error) {
	return nil, nil
}
func (f *fakeSession) ListResourceTemplates(context.Context) ([]ResourceTemplateDescriptor, error) {
	return nil, nil
}
func (f *fakeSession) CallTool(context.Context, string, any) (CallResult, error) {
	return CallResult{}, nil
}
func (f *fakeSession) Close() error {
	f.closed.Store(true)
	return nil
}

func newTestManager() *Manager {
	return &Manager{
		definitions: map[string]ServerDefinition{},
		runtimes:    map[string]*serverRuntime{},
		statuses:    map[string]ServerStatus{},
	}
}

// A server that finishes connecting after Close has run must not be published:
// Close already snapshotted the runtimes, so a published-late runtime would
// never be closed and its child process would leak. publishRuntime reports the
// rejection so startServer can close the orphaned session itself.
func TestPublishRuntimeRejectedAfterClose(t *testing.T) {
	m := newTestManager()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	runtime := &serverRuntime{definition: ServerDefinition{Name: "srv", Transport: TransportStdio}}
	status, published := m.publishRuntime(runtime, "sess-1", nil)
	if published {
		t.Fatal("publishRuntime reported published=true after the manager was closed")
	}
	if status.Connected {
		t.Fatalf("status reported connected for a rejected runtime: %+v", status)
	}

	m.mu.RLock()
	_, present := m.runtimes["srv"]
	m.mu.RUnlock()
	if present {
		t.Fatal("runtime was stored despite the manager being closed")
	}
}

// On an open manager, publishRuntime stores the runtime and Close then closes
// its session — the normal ownership path the rejection case falls back from.
func TestCloseClosesPublishedSessions(t *testing.T) {
	m := newTestManager()
	session := &fakeSession{id: "sess-1"}
	runtime := &serverRuntime{
		definition: ServerDefinition{Name: "srv", Transport: TransportStdio},
		session:    session,
	}

	if _, published := m.publishRuntime(runtime, session.ID(), nil); !published {
		t.Fatal("publishRuntime reported published=false on an open manager")
	}
	m.mu.RLock()
	_, present := m.runtimes["srv"]
	m.mu.RUnlock()
	if !present {
		t.Fatal("runtime was not stored on an open manager")
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !session.closed.Load() {
		t.Fatal("Close did not close the published session")
	}
}

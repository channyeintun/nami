package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *LocalStore {
	t.Helper()
	return NewLocalStore(t.TempDir())
}

func saveArtifact(t *testing.T, store *LocalStore, req SaveRequest) ArtifactVersion {
	t.Helper()
	version, err := store.Save(context.Background(), req)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return version
}

func TestSaveCreatesArtifactWithDefaults(t *testing.T) {
	store := newTestStore(t)
	version := saveArtifact(t, store, SaveRequest{
		Kind:    KindWalkthrough,
		Content: []byte("# Walkthrough"),
	})

	if version.Version != 1 {
		t.Fatalf("Version = %d, want 1", version.Version)
	}
	if version.ArtifactID == "" {
		t.Fatal("Save did not assign an id")
	}

	loaded, err := store.Load(context.Background(), LoadRequest{ID: version.ArtifactID})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Scope != ScopeSession {
		t.Errorf("Scope = %q, want the session default", loaded.Scope)
	}
	if loaded.Title != string(KindWalkthrough) {
		t.Errorf("Title = %q, want the kind as the fallback title", loaded.Title)
	}
	if loaded.MimeType != MarkdownMimeType {
		t.Errorf("MimeType = %q, want the markdown default", loaded.MimeType)
	}

	content, err := os.ReadFile(loaded.ContentPath)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != "# Walkthrough" {
		t.Fatalf("content = %q", content)
	}
}

func TestSaveRequiresKind(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Save(context.Background(), SaveRequest{Content: []byte("x")}); err == nil {
		t.Fatal("Save accepted a request with no kind")
	}
}

func TestSaveVersionsExistingArtifactAndInheritsFields(t *testing.T) {
	store := newTestStore(t)
	first := saveArtifact(t, store, SaveRequest{
		ID:       "plan-1",
		Kind:     KindImplementationPlan,
		Title:    "Original title",
		Source:   "planner",
		MimeType: "text/markdown",
		Content:  []byte("v1"),
		Metadata: map[string]any{"stage": "draft", "keep": "yes"},
	})

	second := saveArtifact(t, store, SaveRequest{
		ID:       "plan-1",
		Content:  []byte("v2"),
		Metadata: map[string]any{"stage": "final"},
	})

	if second.Version != 2 {
		t.Fatalf("Version = %d, want 2", second.Version)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Error("CreatedAt should carry over from the first version")
	}

	loaded, err := store.Load(context.Background(), LoadRequest{ID: "plan-1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "Original title" || loaded.Source != "planner" {
		t.Fatalf("inherited fields lost: %+v", loaded)
	}
	if loaded.Metadata["stage"] != "final" || loaded.Metadata["keep"] != "yes" {
		t.Fatalf("metadata = %+v, want the merge of both saves", loaded.Metadata)
	}
}

func TestSaveRejectsKindChange(t *testing.T) {
	store := newTestStore(t)
	saveArtifact(t, store, SaveRequest{ID: "a1", Kind: KindTaskList, Content: []byte("v1")})

	_, err := store.Save(context.Background(), SaveRequest{ID: "a1", Kind: KindDiagram, Content: []byte("v2")})
	if err == nil || !strings.Contains(err.Error(), "kind mismatch") {
		t.Fatalf("error = %v, want a kind mismatch", err)
	}
}

func TestLoadSpecificVersion(t *testing.T) {
	store := newTestStore(t)
	saveArtifact(t, store, SaveRequest{ID: "a1", Kind: KindTaskList, Content: []byte("v1")})
	saveArtifact(t, store, SaveRequest{ID: "a1", Content: []byte("v2")})

	older, err := store.Load(context.Background(), LoadRequest{ID: "a1", Version: 1})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if older.Version != 1 {
		t.Fatalf("Version = %d, want 1", older.Version)
	}
	content, err := os.ReadFile(older.ContentPath)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != "v1" {
		t.Fatalf("content = %q, want the first version", content)
	}

	if _, err := store.Load(context.Background(), LoadRequest{ID: "a1", Version: 9}); err == nil {
		t.Fatal("Load returned a missing version without an error")
	}
	if _, err := store.Load(context.Background(), LoadRequest{ID: "missing"}); err == nil {
		t.Fatal("Load returned a missing artifact without an error")
	}
}

func TestListFiltersAndSorts(t *testing.T) {
	store := newTestStore(t)
	saveArtifact(t, store, SaveRequest{ID: "plan", Kind: KindImplementationPlan, Title: "Plan", Content: []byte("x")})
	saveArtifact(t, store, SaveRequest{ID: "walk", Kind: KindWalkthrough, Title: "Walkthrough", Scope: ScopeUser, Content: []byte("x")})

	all, err := store.List(context.Background(), ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List returned %d refs, want 2", len(all))
	}

	byKind, err := store.List(context.Background(), ListRequest{Kind: KindWalkthrough})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(byKind) != 1 || byKind[0].ID != "walk" {
		t.Fatalf("kind filter = %+v", byKind)
	}

	byScope, err := store.List(context.Background(), ListRequest{Scope: ScopeUser})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(byScope) != 1 || byScope[0].ID != "walk" {
		t.Fatalf("scope filter = %+v", byScope)
	}
}

func TestListOnMissingBaseDir(t *testing.T) {
	store := NewLocalStore(filepath.Join(t.TempDir(), "does-not-exist"))
	refs, err := store.List(context.Background(), ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if refs != nil {
		t.Fatalf("refs = %+v, want nil", refs)
	}
}

func TestVersionsListsNewestFirst(t *testing.T) {
	store := newTestStore(t)
	saveArtifact(t, store, SaveRequest{ID: "a1", Kind: KindTaskList, Content: []byte("v1")})
	saveArtifact(t, store, SaveRequest{ID: "a1", Content: []byte("v2")})
	saveArtifact(t, store, SaveRequest{ID: "a1", Content: []byte("v3")})

	versions, err := store.Versions(context.Background(), VersionsRequest{ID: "a1"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("versions = %d, want 3", len(versions))
	}
	if versions[0].Version != 3 || versions[2].Version != 1 {
		t.Fatalf("versions = %+v, want newest first", versions)
	}

	if _, err := store.Versions(context.Background(), VersionsRequest{ID: "missing"}); err == nil {
		t.Fatal("Versions returned no error for a missing artifact")
	}
}

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	saveArtifact(t, store, SaveRequest{ID: "a1", Kind: KindTaskList, Content: []byte("v1")})

	if err := store.Delete(context.Background(), DeleteRequest{ID: "a1"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Load(context.Background(), LoadRequest{ID: "a1"}); err == nil {
		t.Fatal("artifact still loads after deletion")
	}
	if err := store.Delete(context.Background(), DeleteRequest{ID: "a1"}); err == nil {
		t.Fatal("deleting a missing artifact returned no error")
	}
}

// The id becomes a directory name under the artifact root, so anything that can
// escape that root has to be rejected before it reaches the filesystem.
func TestSanitizeArtifactIDRejectsTraversal(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"..",
		".",
		"../escape",
		"nested/id",
		`nested\id`,
		"/absolute",
		"./relative",
		"trailing/",
	}
	for _, id := range invalid {
		if got, err := sanitizeArtifactID(id); err == nil {
			t.Errorf("sanitizeArtifactID(%q) = %q, want an error", id, got)
		}
	}

	valid := []string{"plan-1", "a1", "artifact_42", "UPPER-case.v2"}
	for _, id := range valid {
		if _, err := sanitizeArtifactID(id); err != nil {
			t.Errorf("sanitizeArtifactID(%q): %v", id, err)
		}
	}
}

func TestStoreOperationsRejectUnsafeIDs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	unsafe := "../escape"

	if _, err := store.Save(ctx, SaveRequest{ID: unsafe, Kind: KindTaskList, Content: []byte("x")}); err == nil {
		t.Error("Save accepted a traversal id")
	}
	if _, err := store.Load(ctx, LoadRequest{ID: unsafe}); err == nil {
		t.Error("Load accepted a traversal id")
	}
	if err := store.Delete(ctx, DeleteRequest{ID: unsafe}); err == nil {
		t.Error("Delete accepted a traversal id")
	}
	if _, err := store.Versions(ctx, VersionsRequest{ID: unsafe}); err == nil {
		t.Error("Versions accepted a traversal id")
	}
}

func TestGenerateIDIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 200)
	for range 200 {
		id := generateID()
		if id == "" || id == "0000000000000000" {
			t.Fatalf("generateID returned %q", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("generateID returned %q twice", id)
		}
		seen[id] = struct{}{}
	}
}

func TestMetadataHelpers(t *testing.T) {
	if got := cloneMetadata(nil); got != nil {
		t.Fatalf("cloneMetadata(nil) = %+v, want nil", got)
	}
	original := map[string]any{"a": 1}
	cloned := cloneMetadata(original)
	cloned["b"] = 2
	if _, ok := original["b"]; ok {
		t.Fatal("cloneMetadata shared the underlying map")
	}

	merged := mergeMetadata(map[string]any{"a": 1, "b": 1}, map[string]any{"b": 2, "c": 3})
	if merged["a"] != 1 || merged["b"] != 2 || merged["c"] != 3 {
		t.Fatalf("mergeMetadata = %+v", merged)
	}
	if got := mergeMetadata(nil, map[string]any{"a": 1}); got["a"] != 1 {
		t.Fatalf("mergeMetadata with a nil base = %+v", got)
	}
}

func TestNormalizeOwnershipMetadata(t *testing.T) {
	session := normalizeOwnershipMetadata(ScopeSession, nil, " session-7 ", " slot-a ")
	if session[MetadataOwnerScope] != string(ScopeSession) {
		t.Errorf("owner scope = %v", session[MetadataOwnerScope])
	}
	if session[MetadataOwnerAuthority] != OwnerAuthorityParentSession {
		t.Errorf("owner authority = %v", session[MetadataOwnerAuthority])
	}
	if session[MetadataOwnerID] != "session-7" || session[MetadataSessionID] != "session-7" {
		t.Errorf("owner id = %v, session id = %v", session[MetadataOwnerID], session[MetadataSessionID])
	}
	if session[MetadataSlot] != "slot-a" {
		t.Errorf("slot = %v", session[MetadataSlot])
	}

	user := normalizeOwnershipMetadata(ScopeUser, map[string]any{"existing": true}, "user-1", "")
	if user[MetadataOwnerAuthority] != OwnerAuthorityUser {
		t.Errorf("owner authority = %v", user[MetadataOwnerAuthority])
	}
	if _, ok := user[MetadataSessionID]; ok {
		t.Error("user-scoped artifacts should not carry a session id")
	}
	if user["existing"] != true {
		t.Error("existing metadata should be preserved")
	}
	if _, ok := user[MetadataSlot]; ok {
		t.Error("a blank slot should not be recorded")
	}
}

func TestSessionOwnerID(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
		want     string
	}{
		{"explicit session id", map[string]any{MetadataSessionID: "s1"}, "s1"},
		{"owner id with session scope", map[string]any{MetadataOwnerScope: string(ScopeSession), MetadataOwnerID: "s2"}, "s2"},
		{"user scope", map[string]any{MetadataOwnerScope: string(ScopeUser), MetadataOwnerID: "u1"}, ""},
		{"foreign authority", map[string]any{
			MetadataOwnerScope:     string(ScopeSession),
			MetadataOwnerAuthority: "someone-else",
			MetadataOwnerID:        "s3",
		}, ""},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionOwnerID(tc.metadata); got != tc.want {
				t.Fatalf("sessionOwnerID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWriteFileAtomicReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := writeFileAtomic(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if err := writeFileAtomic(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("content = %q", data)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the target file", len(entries))
	}
}

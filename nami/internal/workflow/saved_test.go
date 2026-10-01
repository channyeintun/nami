package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSavedScript(t *testing.T, dir string, file string, source string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func savedSource(description string) string {
	return "export const meta = { name: 'saved', description: '" + description + "', whenToUse: 'when testing' }\nreturn 1\n"
}

// A project workflow has to win over a user workflow of the same name, even
// when the names differ in case: otherwise a repository could not pin the
// version of a workflow its checks depend on.
func TestLoadSavedLetsProjectWorkflowsOverrideUserWorkflows(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	userDir := filepath.Join(root, "user")
	projectReview := writeSavedScript(t, projectDir, "Review.js", savedSource("project review"))
	writeSavedScript(t, userDir, "review.js", savedSource("user review"))
	userOnly := writeSavedScript(t, userDir, "triage.js", savedSource("user triage"))
	writeSavedScript(t, userDir, "notes.txt", "not a workflow")
	if err := os.MkdirAll(filepath.Join(userDir, "nested.js"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	saved, err := LoadSaved(projectDir, userDir)
	if err != nil {
		t.Fatalf("LoadSaved() error = %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("saved = %+v, want review and triage", saved)
	}
	review, ok := FindSaved(saved, " REVIEW ")
	if !ok {
		t.Fatalf("FindSaved(review) found nothing in %+v", saved)
	}
	if review.Scope != SavedScopeProject || review.Path != projectReview || review.Meta.Description != "project review" {
		t.Fatalf("review = %+v, want the project copy", review)
	}
	triage, ok := FindSaved(saved, "triage")
	if !ok || triage.Scope != SavedScopeUser || triage.Path != userOnly || triage.Meta.WhenToUse != "when testing" {
		t.Fatalf("triage = %+v, %v", triage, ok)
	}
	if _, ok := FindSaved(saved, "notes"); ok {
		t.Fatal("a non-.js file was loaded as a workflow")
	}
}

// Nobody has to create workflow directories before using nami, so a missing
// one is simply empty rather than an error on every turn.
func TestLoadSavedTreatsMissingDirectoriesAsEmpty(t *testing.T) {
	root := t.TempDir()
	saved, err := LoadSaved(filepath.Join(root, "absent-project"), filepath.Join(root, "absent-user"))
	if err != nil || saved != nil {
		t.Fatalf("LoadSaved() = %+v, %v; want nil, nil", saved, err)
	}
	saved, err = LoadSaved("", "")
	if err != nil || saved != nil {
		t.Fatalf("LoadSaved(\"\", \"\") = %+v, %v; want nil, nil", saved, err)
	}
}

// One broken file must not hide every other saved workflow, but its error
// still has to reach whoever can fix it.
func TestLoadSavedReturnsGoodWorkflowsAlongsideAFileThatFailsToParse(t *testing.T) {
	dir := t.TempDir()
	writeSavedScript(t, dir, "good.js", savedSource("works"))
	broken := writeSavedScript(t, dir, "broken.js", "export const meta = { name: 'broken', description: 'd' }\nreturn (\n")

	saved, err := LoadSaved(dir, "")
	if err == nil || !strings.Contains(err.Error(), broken) {
		t.Fatalf("err = %v, want one naming %s", err, broken)
	}
	if len(saved) != 1 || saved[0].Name != "good" || saved[0].Scope != SavedScopeProject {
		t.Fatalf("saved = %+v, want only good", saved)
	}
}

package workflow

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Saved workflow scopes. A project workflow lives in the repository and
// overrides a user workflow of the same name, the way project config
// overrides user config.
const (
	SavedScopeProject = "project"
	SavedScopeUser    = "user"
)

// Saved is a workflow script kept on disk so it can be run by name.
type Saved struct {
	// Name is the file's basename without .js. It is what workflow() and
	// the workflow tool's name input match, so it is the name a user sees in
	// the directory listing, not whatever the script's meta says.
	Name  string
	Scope string
	Path  string
	Meta  Meta
}

// LoadSaved reads the saved workflows in projectDir and userDir: flat *.js
// files, no subdirectories. A project workflow replaces a user workflow of
// the same name. A missing directory has no workflows.
//
// A file that cannot be read or parsed is left out and its error joined into
// the returned error, while every good workflow is still returned: one broken
// file must not hide the rest.
func LoadSaved(projectDir string, userDir string) ([]Saved, error) {
	byName := map[string]Saved{}
	var errs []error
	// User first, so a project workflow of the same name overwrites it.
	for _, dir := range []struct{ path, scope string }{
		{userDir, SavedScopeUser},
		{projectDir, SavedScopeProject},
	} {
		found, err := loadSavedDir(dir.path, dir.scope)
		if err != nil {
			errs = append(errs, err)
		}
		for _, saved := range found {
			byName[savedKey(saved.Name)] = saved
		}
	}
	if len(byName) == 0 {
		return nil, errors.Join(errs...)
	}

	saved := make([]Saved, 0, len(byName))
	for _, entry := range byName {
		saved = append(saved, entry)
	}
	slices.SortFunc(saved, func(a, b Saved) int {
		return cmp.Compare(savedKey(a.Name), savedKey(b.Name))
	})
	return saved, errors.Join(errs...)
}

func loadSavedDir(dir string, scope string) ([]Saved, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved workflows in %s: %w", dir, err)
	}

	var saved []Saved
	var errs []error
	for _, entry := range entries {
		name, isScript := strings.CutSuffix(entry.Name(), ".js")
		if !isScript || entry.IsDir() || strings.TrimSpace(name) == "" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		script, err := ReadScript(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("saved workflow %s: %w", path, err))
			continue
		}
		saved = append(saved, Saved{
			Name:  strings.TrimSpace(name),
			Scope: scope,
			Path:  path,
			Meta:  script.Meta,
		})
	}
	return saved, errors.Join(errs...)
}

// FindSaved looks a saved workflow up by name, ignoring case and surrounding
// space.
func FindSaved(saved []Saved, name string) (Saved, bool) {
	key := savedKey(name)
	for _, entry := range saved {
		if savedKey(entry.Name) == key {
			return entry, true
		}
	}
	return Saved{}, false
}

func savedKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ReadScript reads and parses a workflow script file.
func ReadScript(path string) (Script, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return Script{}, err
	}
	return ParseScript(path, string(source))
}

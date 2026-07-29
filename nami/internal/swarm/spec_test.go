package swarm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSpec(t *testing.T, spec Spec) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".nami")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The project root is located by walking up to a .git directory.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	path := filepath.Join(dir, "swarm.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func minimalRole(name string) RoleSpec {
	return RoleSpec{Name: name, Purpose: "does " + name + " work"}
}

func TestResolveAppliesDefaults(t *testing.T) {
	resolved, err := Spec{Roles: []RoleSpec{minimalRole("coder")}}.Resolve("/repo", "/repo/.nami/swarm.json")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Version != 1 {
		t.Errorf("Version = %d, want 1", resolved.Version)
	}
	role := resolved.Roles[0]
	if role.WorkspaceStrategy != WorkspaceShared {
		t.Errorf("workspace = %q, want %q", role.WorkspaceStrategy, WorkspaceShared)
	}
	if role.QueuePolicy != QueueFIFO {
		t.Errorf("queue policy = %q, want %q", role.QueuePolicy, QueueFIFO)
	}
	if role.PermissionProfile != PermissionProfileInherit {
		t.Errorf("permission profile = %q, want %q", role.PermissionProfile, PermissionProfileInherit)
	}
	if role.SubagentType != "Explore" {
		t.Errorf("subagent type = %q, want Explore", role.SubagentType)
	}
}

func TestResolveNormalizesAliases(t *testing.T) {
	resolved, err := Spec{Roles: []RoleSpec{{
		Name:         "  Reviewer  ",
		Purpose:      "reviews",
		SubagentType: "General-Purpose",
		Workspace:    "WORKTREE",
		QueuePolicy:  "batch_review",
		Permission:   "read_only",
	}}}.Resolve("/repo", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	role := resolved.Roles[0]
	if role.Name != "reviewer" {
		t.Errorf("name = %q, want reviewer", role.Name)
	}
	if role.SubagentType != "general-purpose" {
		t.Errorf("subagent type = %q", role.SubagentType)
	}
	if role.WorkspaceStrategy != WorkspaceWorktree {
		t.Errorf("workspace = %q", role.WorkspaceStrategy)
	}
	if role.QueuePolicy != QueueBatchReview {
		t.Errorf("queue policy = %q", role.QueuePolicy)
	}
	if role.PermissionProfile != PermissionProfileReadOnly {
		t.Errorf("permission profile = %q", role.PermissionProfile)
	}
}

func TestResolveRejectsInvalidSpecs(t *testing.T) {
	cases := map[string]Spec{
		"no roles":            {},
		"unsupported version": {Version: 7, Roles: []RoleSpec{minimalRole("coder")}},
		"missing name":        {Roles: []RoleSpec{{Purpose: "x"}}},
		"bad name":            {Roles: []RoleSpec{{Name: "Bad Name!", Purpose: "x"}}},
		"missing purpose":     {Roles: []RoleSpec{{Name: "coder"}}},
		"bad subagent":        {Roles: []RoleSpec{{Name: "coder", Purpose: "x", SubagentType: "wizard"}}},
		"bad workspace":       {Roles: []RoleSpec{{Name: "coder", Purpose: "x", Workspace: "cloud"}}},
		"bad queue policy":    {Roles: []RoleSpec{{Name: "coder", Purpose: "x", QueuePolicy: "lifo"}}},
		"bad permission":      {Roles: []RoleSpec{{Name: "coder", Purpose: "x", Permission: "root"}}},
		"bad model provider":  {Roles: []RoleSpec{{Name: "coder", Purpose: "x", Model: "nosuchprovider/model"}}},
		"model without name":  {Roles: []RoleSpec{{Name: "coder", Purpose: "x", Model: "anthropic/"}}},
		"duplicate roles": {Roles: []RoleSpec{
			minimalRole("coder"),
			{Name: "CODER", Purpose: "duplicate"},
		}},
		"duplicate allow tools": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", AllowTools: []string{"bash", "bash"},
		}}},
		"empty allow tool entry": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", AllowTools: []string{"  "},
		}}},
		"allow and deny overlap": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", AllowTools: []string{"bash"}, DenyTools: []string{"bash"},
		}}},
		"unknown handoff field": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", Handoff: HandoffSpec{RequiredFields: []string{"vibes"}},
		}}},
		"duplicate handoff field": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", Handoff: HandoffSpec{RequiredFields: []string{"summary", "summary"}},
		}}},
		"handoff target missing": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", Handoff: HandoffSpec{Targets: []string{"nobody"}},
		}}},
		"handoff target self": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", Handoff: HandoffSpec{Targets: []string{"coder"}},
		}}},
		"invalid handoff target name": {Roles: []RoleSpec{{
			Name: "coder", Purpose: "x", Handoff: HandoffSpec{Targets: []string{"Not A Role"}},
		}}},
	}

	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := spec.Resolve("/repo", "/repo/.nami/swarm.json")
			if err == nil {
				t.Fatalf("Resolve(%+v) succeeded, want a validation error", spec)
			}
			validationErr, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("error = %T (%v), want *ValidationError", err, err)
			}
			if len(validationErr.Problems) == 0 {
				t.Fatal("ValidationError carries no problems")
			}
			if !strings.Contains(validationErr.Error(), "invalid swarm spec") {
				t.Fatalf("Error() = %q", validationErr.Error())
			}
		})
	}
}

func TestResolveAcceptsHandoffGraph(t *testing.T) {
	resolved, err := Spec{Roles: []RoleSpec{
		{Name: "architect", Purpose: "plans", Handoff: HandoffSpec{Required: true, Targets: []string{"coder"}}},
		{Name: "coder", Purpose: "writes code", Handoff: HandoffSpec{Targets: []string{"reviewer"}}},
		{Name: "reviewer", Purpose: "reviews"},
	}}.Resolve("/repo", "/repo/.nami/swarm.json")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	architect := resolved.Roles[0]
	// A required handoff with no explicit fields falls back to the defaults.
	if len(architect.Handoff.RequiredFields) != len(defaultRequiredHandoffFields) {
		t.Fatalf("required fields = %v, want the defaults", architect.Handoff.RequiredFields)
	}
	coder := resolved.Roles[1]
	if coder.Handoff.Required {
		t.Error("coder handoff should not be required")
	}
	if len(coder.Handoff.RequiredFields) != 0 {
		t.Errorf("required fields = %v, want none", coder.Handoff.RequiredFields)
	}
}

func TestResolvedSpecRoleLookupIsCaseInsensitive(t *testing.T) {
	resolved, err := Spec{Roles: []RoleSpec{minimalRole("coder")}}.Resolve("/repo", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := resolved.Role("  CODER "); !ok {
		t.Fatal("Role lookup should normalize the name")
	}
	if _, ok := resolved.Role("missing"); ok {
		t.Fatal("Role returned a match for an unknown role")
	}
	if _, ok := resolved.Role("   "); ok {
		t.Fatal("Role returned a match for a blank name")
	}
}

func TestSummaryMarkdownDescribesEveryRole(t *testing.T) {
	resolved, err := Spec{Roles: []RoleSpec{{
		Name:       "coder",
		Purpose:    "writes code",
		Model:      "anthropic/claude-opus-5",
		AllowTools: []string{"bash"},
		DenyTools:  []string{"web_fetch"},
		Metadata:   RoleMetadata{Owner: "platform"},
		Handoff:    HandoffSpec{Required: true},
	}}}.Resolve("/repo", "/repo/.nami/swarm.json")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	markdown := resolved.SummaryMarkdown()
	for _, want := range []string{
		"# Swarm Spec",
		"- Version: 1",
		"- Project root: /repo",
		"## Role: coder",
		"- Purpose: writes code",
		"- Model: anthropic/claude-opus-5",
		"- Owner: platform",
		"- Allowed tools: bash",
		"- Denied tools: web_fetch",
		"- Handoff required: true",
		"- Handoff targets: none",
		"summary, verification",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("summary missing %q:\n%s", want, markdown)
		}
	}
}

func TestSummaryMarkdownReportsSessionDefaultModel(t *testing.T) {
	resolved, err := Spec{Roles: []RoleSpec{minimalRole("coder")}}.Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(resolved.SummaryMarkdown(), "- Model: session default") {
		t.Fatal("summary should call out the session default model")
	}
}

func TestLoadProjectSpecFromPath(t *testing.T) {
	path := writeSpec(t, Spec{Roles: []RoleSpec{minimalRole("coder")}})
	resolved, err := LoadProjectSpecFromPath(path)
	if err != nil {
		t.Fatalf("LoadProjectSpecFromPath: %v", err)
	}
	if len(resolved.Roles) != 1 || resolved.Roles[0].Name != "coder" {
		t.Fatalf("roles = %+v", resolved.Roles)
	}
	if resolved.ProjectRoot != filepath.Dir(filepath.Dir(path)) {
		t.Fatalf("project root = %q", resolved.ProjectRoot)
	}
}

func TestLoadProjectSpecFromPathErrors(t *testing.T) {
	if _, err := LoadProjectSpecFromPath("   "); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blank path error = %v, want ErrNotExist", err)
	}
	if _, err := LoadProjectSpecFromPath(filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v, want ErrNotExist", err)
	}

	empty := filepath.Join(t.TempDir(), "swarm.json")
	if err := os.WriteFile(empty, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadProjectSpecFromPath(empty); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty file error = %v, want ErrNotExist", err)
	}

	malformed := filepath.Join(t.TempDir(), "swarm.json")
	if err := os.WriteFile(malformed, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadProjectSpecFromPath(malformed); err == nil {
		t.Fatal("malformed spec parsed without error")
	}
}

func TestLoadRoleWorkspaceStrategy(t *testing.T) {
	path := writeSpec(t, Spec{Roles: []RoleSpec{{
		Name: "coder", Purpose: "writes", Workspace: "worktree",
	}}})
	projectRoot := filepath.Dir(filepath.Dir(path))

	strategy, err := LoadRoleWorkspaceStrategy(projectRoot, "coder")
	if err != nil {
		t.Fatalf("LoadRoleWorkspaceStrategy: %v", err)
	}
	if strategy != WorkspaceWorktree {
		t.Fatalf("strategy = %q, want worktree", strategy)
	}
	if _, err := LoadRoleWorkspaceStrategy(projectRoot, "missing"); err == nil {
		t.Fatal("unknown role returned no error")
	}
}

func TestParseWorkspaceStrategy(t *testing.T) {
	for input, want := range map[string]WorkspaceStrategy{
		"":          WorkspaceShared,
		"shared":    WorkspaceShared,
		"WORKTREE":  WorkspaceWorktree,
		" snapshot": WorkspaceSnapshot,
	} {
		got, ok := ParseWorkspaceStrategy(input)
		if !ok || got != want {
			t.Errorf("ParseWorkspaceStrategy(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	if _, ok := ParseWorkspaceStrategy("cloud"); ok {
		t.Error("ParseWorkspaceStrategy accepted an unknown strategy")
	}
}

func TestValidationErrorMessages(t *testing.T) {
	var nilErr *ValidationError
	if got := nilErr.Error(); got != "invalid swarm spec" {
		t.Errorf("nil ValidationError = %q", got)
	}
	withoutPath := &ValidationError{Problems: []string{"a", "b"}}
	if got := withoutPath.Error(); got != "invalid swarm spec: a; b" {
		t.Errorf("Error() = %q", got)
	}
	withPath := &ValidationError{Path: "/repo/.nami/swarm.json", Problems: []string{"a"}}
	if !strings.Contains(withPath.Error(), "/repo/.nami/swarm.json") {
		t.Errorf("Error() = %q, want the path", withPath.Error())
	}
}

func TestListOverlapIsSorted(t *testing.T) {
	got := listOverlap([]string{"z", "a", "m"}, []string{"m", "a"})
	if len(got) != 2 || got[0] != "a" || got[1] != "m" {
		t.Fatalf("listOverlap = %#v, want [a m]", got)
	}
	if overlap := listOverlap([]string{"a"}, []string{"b"}); len(overlap) != 0 {
		t.Fatalf("listOverlap = %#v, want empty", overlap)
	}
}

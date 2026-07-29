package swarm

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// FuzzSpecResolve feeds arbitrary JSON through spec resolution. A project spec
// is user-authored, so resolution must either produce a fully normalized spec
// or a *ValidationError listing what to fix — never a panic, and never a
// half-normalized role.
func FuzzSpecResolve(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"version":1,"roles":[{"name":"coder","purpose":"writes"}]}`,
		`{"roles":[{"name":"coder","purpose":"x","handoff":{"required":true,"targets":["reviewer"]}},{"name":"reviewer","purpose":"y"}]}`,
		`{"version":2,"roles":[]}`,
		`{"roles":[{"name":"UPPER","purpose":"x","workspace":"worktree","queue_policy":"latest_wins","permission_profile":"read_only"}]}`,
		`{"roles":[{"name":"a","purpose":"x","allow_tools":["bash","bash"]}]}`,
		`{"roles":[{"name":"a","purpose":"x","model":"anthropic/claude-opus-5"}]}`,
		`{"roles":[{"name":"a","purpose":"x","handoff":{"required_fields":["summary","bogus"]}}]}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, encoded string) {
		var spec Spec
		if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
			return
		}

		resolved, err := spec.Resolve("/repo", "/repo/.nami/swarm.json")
		if err != nil {
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Fatalf("Resolve returned %T, want *ValidationError", err)
			}
			return
		}

		if resolved.Version != 1 {
			t.Fatalf("resolved version = %d, want 1", resolved.Version)
		}
		if len(resolved.Roles) == 0 {
			t.Fatal("resolution succeeded with no roles")
		}

		names := make(map[string]struct{}, len(resolved.Roles))
		for _, role := range resolved.Roles {
			assertRoleIsNormalized(t, role)
			if _, ok := names[role.Name]; ok {
				t.Fatalf("duplicate role %q survived resolution", role.Name)
			}
			names[role.Name] = struct{}{}
		}
		for _, role := range resolved.Roles {
			for _, target := range role.Handoff.Targets {
				if target == role.Name {
					t.Fatalf("role %q kept a handoff target pointing at itself", role.Name)
				}
				if _, ok := names[target]; !ok {
					t.Fatalf("role %q hands off to unknown role %q", role.Name, target)
				}
			}
		}
	})
}

func assertRoleIsNormalized(t *testing.T, role ResolvedRole) {
	t.Helper()
	if role.Name == "" || !roleNamePattern.MatchString(role.Name) {
		t.Fatalf("role name %q is not normalized", role.Name)
	}
	if strings.TrimSpace(role.Purpose) == "" {
		t.Fatalf("role %q has no purpose", role.Name)
	}
	switch role.WorkspaceStrategy {
	case WorkspaceShared, WorkspaceWorktree, WorkspaceSnapshot:
	default:
		t.Fatalf("role %q has workspace %q", role.Name, role.WorkspaceStrategy)
	}
	switch role.QueuePolicy {
	case QueueFIFO, QueueBatchReview, QueueLatestWins:
	default:
		t.Fatalf("role %q has queue policy %q", role.Name, role.QueuePolicy)
	}
	switch role.PermissionProfile {
	case PermissionProfileInherit, PermissionProfileReadOnly, PermissionProfileExecute, PermissionProfileWrite:
	default:
		t.Fatalf("role %q has permission profile %q", role.Name, role.PermissionProfile)
	}
	for _, field := range role.Handoff.RequiredFields {
		if _, ok := allowedHandoffFields[field]; !ok {
			t.Fatalf("role %q requires unsupported handoff field %q", role.Name, field)
		}
	}
	for _, tool := range role.AllowTools {
		for _, denied := range role.DenyTools {
			if tool == denied {
				t.Fatalf("role %q both allows and denies %q", role.Name, tool)
			}
		}
	}
}

// FuzzPrepareHandoff checks that any handoff the engine accepts is fully
// normalized: trimmed roles, a status the inbox understands, and a history.
func FuzzPrepareHandoff(f *testing.F) {
	f.Add("coder", "reviewer", "summary text", "pending")
	f.Add("  Coder  ", "REVIEWER", "  s  ", "in-progress")
	f.Add("", "reviewer", "s", "")
	f.Add("coder", "", "s", "bogus")
	f.Add("coder", "reviewer", "   ", "completed")

	f.Fuzz(func(t *testing.T, source, target, summary, status string) {
		handoff, err := PrepareHandoff(Handoff{
			SourceRole: source,
			TargetRole: target,
			Summary:    summary,
			Status:     HandoffStatus(status),
		})
		if err != nil {
			return
		}

		if handoff.SourceRole != normalizeRoleName(source) {
			t.Fatalf("source role = %q, want %q", handoff.SourceRole, normalizeRoleName(source))
		}
		if handoff.TargetRole != normalizeRoleName(target) {
			t.Fatalf("target role = %q, want %q", handoff.TargetRole, normalizeRoleName(target))
		}
		if handoff.SourceRole == "" || handoff.TargetRole == "" || handoff.Summary == "" {
			t.Fatalf("accepted handoff is missing a required field: %+v", handoff)
		}
		if !IsValidHandoffStatus(handoff.Status) {
			t.Fatalf("accepted handoff has status %q", handoff.Status)
		}
		if handoff.ID == "" || handoff.ArtifactID == "" {
			t.Fatalf("accepted handoff has no id: %+v", handoff)
		}
		if len(handoff.History) == 0 {
			t.Fatal("accepted handoff has no history entry")
		}
		if handoff.CreatedAt.IsZero() || handoff.UpdatedAt.IsZero() {
			t.Fatalf("accepted handoff has zero timestamps: %+v", handoff)
		}
	})
}

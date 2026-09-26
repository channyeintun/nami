package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/channyeintun/nami/internal/session"
)

type HandoffStatus string

const (
	HandoffStatusPending    HandoffStatus = "pending"
	HandoffStatusAcked      HandoffStatus = "acked"
	HandoffStatusInProgress HandoffStatus = "in_progress"
	HandoffStatusCompleted  HandoffStatus = "completed"
	HandoffStatusBlocked    HandoffStatus = "blocked"
	HandoffStatusSuperseded HandoffStatus = "superseded"
)

type Handoff struct {
	ID           string               `json:"id"`
	ArtifactID   string               `json:"artifact_id,omitempty"`
	SourceRole   string               `json:"source_role"`
	TargetRole   string               `json:"target_role"`
	Summary      string               `json:"summary"`
	ChangedFiles []string             `json:"changed_files,omitempty"`
	CommandsRun  []string             `json:"commands_run,omitempty"`
	Verification string               `json:"verification,omitempty"`
	Risks        []string             `json:"risks,omitempty"`
	NextAction   string               `json:"next_action,omitempty"`
	Status       HandoffStatus        `json:"status"`
	StatusNote   string               `json:"status_note,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	History      []HandoffStatusEntry `json:"history,omitempty"`
}

type HandoffStatusEntry struct {
	Status HandoffStatus `json:"status"`
	Note   string        `json:"note,omitempty"`
	At     time.Time     `json:"at"`
}

type Inbox struct {
	Handoffs []Handoff `json:"handoffs,omitempty"`
}

type DequeueResult struct {
	Handoffs   []Handoff
	Superseded []Handoff
}

var inboxMu sync.Mutex

const inboxRelativePath = "swarm/inbox.json"

func NewHandoffID() string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("handoff-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("handoff-%d-%s", time.Now().UTC().Unix(), hex.EncodeToString(buffer))
}

func NormalizeHandoffStatus(value string) HandoffStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "pending":
		return HandoffStatusPending
	case "acked":
		return HandoffStatusAcked
	case "in_progress", "in-progress":
		return HandoffStatusInProgress
	case "completed":
		return HandoffStatusCompleted
	case "blocked":
		return HandoffStatusBlocked
	case "superseded":
		return HandoffStatusSuperseded
	default:
		return HandoffStatus("")
	}
}

func IsValidHandoffStatus(status HandoffStatus) bool {
	switch status {
	case HandoffStatusPending, HandoffStatusAcked, HandoffStatusInProgress, HandoffStatusCompleted, HandoffStatusBlocked, HandoffStatusSuperseded:
		return true
	default:
		return false
	}
}

func PrepareHandoff(handoff Handoff) (Handoff, error) {
	return normalizeHandoffRecord(handoff, time.Now().UTC())
}

func LoadRoleHandoffInstructions(cwd string, role string) (string, error) {
	spec, err := LoadProjectSpec(cwd)
	if err != nil {
		return "", err
	}
	resolvedRole, ok := spec.Role(role)
	if !ok {
		return "", fmt.Errorf("swarm role %q is not defined in %s", strings.TrimSpace(role), spec.Path)
	}
	if !resolvedRole.Handoff.Required && len(resolvedRole.Handoff.Targets) == 0 && len(resolvedRole.Handoff.RequiredFields) == 0 {
		return "", os.ErrNotExist
	}
	return renderRoleHandoffInstructions(resolvedRole), nil
}

func ValidateHandoffAgainstSpec(spec ResolvedSpec, handoff Handoff) error {
	sourceRole, ok := spec.Role(handoff.SourceRole)
	if !ok {
		return fmt.Errorf("swarm source role %q is not defined in %s", handoff.SourceRole, spec.Path)
	}
	if _, ok := spec.Role(handoff.TargetRole); !ok {
		return fmt.Errorf("swarm target role %q is not defined in %s", handoff.TargetRole, spec.Path)
	}
	if len(sourceRole.Handoff.Targets) > 0 {
		allowed := slices.Contains(sourceRole.Handoff.Targets, handoff.TargetRole)
		if !allowed {
			return fmt.Errorf("swarm role %q may only hand off to: %s", handoff.SourceRole, strings.Join(sourceRole.Handoff.Targets, ", "))
		}
	}
	if !sourceRole.Handoff.Required && len(sourceRole.Handoff.RequiredFields) == 0 {
		return nil
	}
	missing := missingRequiredFields(handoff, sourceRole.Handoff.RequiredFields)
	if len(missing) > 0 {
		labels := make([]string, 0, len(missing))
		for _, field := range missing {
			labels = append(labels, string(field))
		}
		return fmt.Errorf("swarm handoff missing required fields for role %q: %s", handoff.SourceRole, strings.Join(labels, ", "))
	}
	return nil
}

func RenderHandoffMarkdown(handoff Handoff) string {
	var b strings.Builder
	b.WriteString("# Handoff\n\n")
	b.WriteString("- ID: ")
	b.WriteString(strings.TrimSpace(handoff.ID))
	b.WriteString("\n- From: ")
	b.WriteString(strings.TrimSpace(handoff.SourceRole))
	b.WriteString("\n- To: ")
	b.WriteString(strings.TrimSpace(handoff.TargetRole))
	b.WriteString("\n- Status: ")
	b.WriteString(string(handoff.Status))
	b.WriteString("\n- Created: ")
	b.WriteString(handoff.CreatedAt.UTC().Format(time.RFC3339))
	b.WriteString("\n- Updated: ")
	b.WriteString(handoff.UpdatedAt.UTC().Format(time.RFC3339))
	b.WriteString("\n")

	writeSection := func(title string, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		b.WriteString("\n## ")
		b.WriteString(title)
		b.WriteString("\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	}

	writeList := func(title string, values []string) {
		if len(values) == 0 {
			return
		}
		b.WriteString("\n## ")
		b.WriteString(title)
		b.WriteString("\n\n")
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(trimmed)
			b.WriteString("\n")
		}
	}

	writeSection("Summary", handoff.Summary)
	writeList("Changed Files", handoff.ChangedFiles)
	writeList("Commands Run", handoff.CommandsRun)
	writeSection("Verification", handoff.Verification)
	writeList("Risks", handoff.Risks)
	writeSection("Next Action", handoff.NextAction)
	writeSection("Status Note", handoff.StatusNote)

	if len(handoff.History) > 0 {
		b.WriteString("\n## History\n\n")
		for _, event := range handoff.History {
			b.WriteString("- ")
			b.WriteString(event.At.UTC().Format(time.RFC3339))
			b.WriteString(": ")
			b.WriteString(string(event.Status))
			if note := strings.TrimSpace(event.Note); note != "" {
				b.WriteString(" - ")
				b.WriteString(note)
			}
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String()) + "\n"
}

func LoadInbox(store *session.Store, sessionID string) (Inbox, error) {
	inboxMu.Lock()
	defer inboxMu.Unlock()
	return loadInboxUnlocked(store, sessionID)
}

func ListHandoffs(store *session.Store, sessionID string, role string, statuses []HandoffStatus) ([]Handoff, error) {
	inbox, err := LoadInbox(store, sessionID)
	if err != nil {
		return nil, err
	}
	role = normalizeRoleName(role)
	allowedStatuses := make(map[HandoffStatus]struct{}, len(statuses))
	for _, status := range statuses {
		if IsValidHandoffStatus(status) {
			allowedStatuses[status] = struct{}{}
		}
	}
	filtered := make([]Handoff, 0, len(inbox.Handoffs))
	for _, handoff := range inbox.Handoffs {
		if role != "" && handoff.TargetRole != role && handoff.SourceRole != role {
			continue
		}
		if len(allowedStatuses) > 0 {
			if _, ok := allowedStatuses[handoff.Status]; !ok {
				continue
			}
		}
		filtered = append(filtered, handoff)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	return filtered, nil
}

func DequeueHandoffs(store *session.Store, sessionID string, role string, policy QueuePolicy) (DequeueResult, error) {
	role = normalizeRoleName(role)
	if role == "" {
		return DequeueResult{}, fmt.Errorf("DequeueHandoffs requires a role")
	}

	inboxMu.Lock()
	defer inboxMu.Unlock()

	inbox, err := loadInboxUnlocked(store, sessionID)
	if err != nil {
		return DequeueResult{}, err
	}

	pending := make([]Handoff, 0, len(inbox.Handoffs))
	for _, handoff := range inbox.Handoffs {
		if handoff.TargetRole != role {
			continue
		}
		if handoff.Status != HandoffStatusPending {
			continue
		}
		pending = append(pending, handoff)
	}
	if len(pending) == 0 {
		return DequeueResult{}, nil
	}

	sort.Slice(pending, func(i, j int) bool {
		if pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].ID < pending[j].ID
		}
		return pending[i].CreatedAt.Before(pending[j].CreatedAt)
	})

	switch policy {
	case QueueBatchReview:
		return DequeueResult{Handoffs: append([]Handoff(nil), pending...)}, nil

	case QueueLatestWins:
		newest := pending[len(pending)-1]
		result := DequeueResult{Handoffs: []Handoff{newest}}
		if len(pending) > 1 {
			now := time.Now().UTC()
			superseded := make([]Handoff, 0, len(pending)-1)
			for _, older := range pending[:len(pending)-1] {
				supersededIdx := inboxIndexByID(inbox.Handoffs, older.ID)
				if supersededIdx < 0 {
					continue
				}
				inbox.Handoffs[supersededIdx].Status = HandoffStatusSuperseded
				inbox.Handoffs[supersededIdx].StatusNote = fmt.Sprintf("superseded by %s under latest-wins policy", newest.ID)
				inbox.Handoffs[supersededIdx].UpdatedAt = now
				inbox.Handoffs[supersededIdx].History = append(inbox.Handoffs[supersededIdx].History, HandoffStatusEntry{
					Status: HandoffStatusSuperseded,
					Note:   inbox.Handoffs[supersededIdx].StatusNote,
					At:     now,
				})
				superseded = append(superseded, inbox.Handoffs[supersededIdx])
			}
			if err := saveInboxUnlocked(store, sessionID, inbox); err != nil {
				return DequeueResult{}, err
			}
			result.Superseded = superseded
		}
		return result, nil

	default:
		return DequeueResult{Handoffs: []Handoff{pending[0]}}, nil
	}
}

func inboxIndexByID(handoffs []Handoff, id string) int {
	for i := range handoffs {
		if handoffs[i].ID == id {
			return i
		}
	}
	return -1
}

func UpsertHandoff(store *session.Store, sessionID string, handoff Handoff) (Handoff, error) {
	inboxMu.Lock()
	defer inboxMu.Unlock()

	inbox, err := loadInboxUnlocked(store, sessionID)
	if err != nil {
		return Handoff{}, err
	}
	normalized, err := normalizeHandoffRecord(handoff, time.Now().UTC())
	if err != nil {
		return Handoff{}, err
	}
	updated := false
	for index := range inbox.Handoffs {
		if inbox.Handoffs[index].ID != normalized.ID {
			continue
		}
		normalized.CreatedAt = inbox.Handoffs[index].CreatedAt
		normalized.History = mergeHistory(inbox.Handoffs[index].History, normalized.History)
		inbox.Handoffs[index] = normalized
		updated = true
		break
	}
	if !updated {
		inbox.Handoffs = append(inbox.Handoffs, normalized)
	}
	if err := saveInboxUnlocked(store, sessionID, inbox); err != nil {
		return Handoff{}, err
	}
	return normalized, nil
}

func UpdateHandoffStatus(store *session.Store, sessionID string, handoffID string, status HandoffStatus, note string) (Handoff, error) {
	inboxMu.Lock()
	defer inboxMu.Unlock()
	inbox, err := loadInboxUnlocked(store, sessionID)
	if err != nil {
		return Handoff{}, err
	}
	status = NormalizeHandoffStatus(string(status))
	if !IsValidHandoffStatus(status) {
		return Handoff{}, fmt.Errorf("invalid handoff status %q", status)
	}
	now := time.Now().UTC()
	for index := range inbox.Handoffs {
		if inbox.Handoffs[index].ID != strings.TrimSpace(handoffID) {
			continue
		}
		inbox.Handoffs[index].Status = status
		inbox.Handoffs[index].StatusNote = strings.TrimSpace(note)
		inbox.Handoffs[index].UpdatedAt = now
		inbox.Handoffs[index].History = append(inbox.Handoffs[index].History, HandoffStatusEntry{Status: status, Note: strings.TrimSpace(note), At: now})
		if err := saveInboxUnlocked(store, sessionID, inbox); err != nil {
			return Handoff{}, err
		}
		return inbox.Handoffs[index], nil
	}
	return Handoff{}, fmt.Errorf("swarm handoff %q was not found", strings.TrimSpace(handoffID))
}

func loadInboxUnlocked(store *session.Store, sessionID string) (Inbox, error) {
	if store == nil {
		return Inbox{}, fmt.Errorf("session store is not configured")
	}
	path := filepath.Join(store.SessionDir(sessionID), inboxRelativePath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Inbox{}, nil
		}
		return Inbox{}, fmt.Errorf("read swarm inbox: %w", err)
	}
	var inbox Inbox
	if err := json.Unmarshal(data, &inbox); err != nil {
		return Inbox{}, fmt.Errorf("decode swarm inbox: %w", err)
	}
	return inbox, nil
}

func saveInboxUnlocked(store *session.Store, sessionID string, inbox Inbox) error {
	if store == nil {
		return fmt.Errorf("session store is not configured")
	}
	path := filepath.Join(store.SessionDir(sessionID), inboxRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create swarm inbox dir: %w", err)
	}
	// Sort a copy: the slice shares its array with the caller, who may still
	// be holding an index into it.
	handoffs := slices.Clone(inbox.Handoffs)
	sort.Slice(handoffs, func(i, j int) bool {
		if handoffs[i].UpdatedAt.Equal(handoffs[j].UpdatedAt) {
			return handoffs[i].ID < handoffs[j].ID
		}
		return handoffs[i].UpdatedAt.After(handoffs[j].UpdatedAt)
	})
	data, err := json.MarshalIndent(Inbox{Handoffs: handoffs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode swarm inbox: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write swarm inbox: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace swarm inbox: %w", err)
	}
	return nil
}

func normalizeHandoffRecord(handoff Handoff, now time.Time) (Handoff, error) {
	handoff.ID = strings.TrimSpace(handoff.ID)
	if handoff.ID == "" {
		handoff.ID = NewHandoffID()
	}
	handoff.ArtifactID = strings.TrimSpace(firstNonEmpty(handoff.ArtifactID, handoff.ID))
	handoff.SourceRole = normalizeRoleName(handoff.SourceRole)
	handoff.TargetRole = normalizeRoleName(handoff.TargetRole)
	handoff.Summary = strings.TrimSpace(handoff.Summary)
	handoff.Verification = strings.TrimSpace(handoff.Verification)
	handoff.NextAction = strings.TrimSpace(handoff.NextAction)
	handoff.StatusNote = strings.TrimSpace(handoff.StatusNote)
	handoff.ChangedFiles = trimUniqueList(handoff.ChangedFiles)
	handoff.CommandsRun = trimUniqueList(handoff.CommandsRun)
	handoff.Risks = trimUniqueList(handoff.Risks)
	handoff.Status = NormalizeHandoffStatus(string(handoff.Status))
	if !IsValidHandoffStatus(handoff.Status) {
		handoff.Status = HandoffStatusPending
	}
	if handoff.SourceRole == "" {
		return Handoff{}, fmt.Errorf("swarm handoff requires source_role")
	}
	if handoff.TargetRole == "" {
		return Handoff{}, fmt.Errorf("swarm handoff requires target_role")
	}
	if handoff.Summary == "" {
		return Handoff{}, fmt.Errorf("swarm handoff requires summary")
	}
	if handoff.CreatedAt.IsZero() {
		handoff.CreatedAt = now
	}
	handoff.UpdatedAt = now
	if len(handoff.History) == 0 {
		handoff.History = []HandoffStatusEntry{{Status: handoff.Status, Note: handoff.StatusNote, At: now}}
	}
	return handoff, nil
}

func mergeHistory(existing []HandoffStatusEntry, updated []HandoffStatusEntry) []HandoffStatusEntry {
	if len(updated) == 0 {
		return append([]HandoffStatusEntry(nil), existing...)
	}
	if len(existing) == 0 {
		return append([]HandoffStatusEntry(nil), updated...)
	}
	merged := append([]HandoffStatusEntry(nil), existing...)
	last := merged[len(merged)-1]
	current := updated[len(updated)-1]
	if last.Status == current.Status && last.Note == current.Note {
		return merged
	}
	return append(merged, current)
}

func renderRoleHandoffInstructions(role ResolvedRole) string {
	var b strings.Builder
	b.WriteString("Swarm handoff policy for role \"")
	b.WriteString(role.Name)
	b.WriteString("\".\n")
	b.WriteString("Use swarm_submit_handoff to create a structured handoff when you finish work for another role. Use swarm_list_inbox to inspect queued handoffs. Use swarm_update_handoff to acknowledge progress or resolve a handoff instead of burying that state in prose.\n")
	if len(role.Handoff.Targets) > 0 {
		b.WriteString("Allowed target roles: ")
		b.WriteString(strings.Join(role.Handoff.Targets, ", "))
		b.WriteString("\n")
	}
	if len(role.Handoff.RequiredFields) > 0 {
		fields := make([]string, 0, len(role.Handoff.RequiredFields))
		for _, field := range role.Handoff.RequiredFields {
			fields = append(fields, string(field))
		}
		b.WriteString("Required handoff fields: ")
		b.WriteString(strings.Join(fields, ", "))
		b.WriteString("\n")
	}
	if role.Handoff.Required {
		b.WriteString("A structured handoff is required when this role finishes delegated work for another role.\n")
	}
	return strings.TrimSpace(b.String())
}

func missingRequiredFields(handoff Handoff, fields []HandoffField) []HandoffField {
	missing := make([]HandoffField, 0, len(fields))
	for _, field := range fields {
		switch field {
		case HandoffFieldSummary:
			if strings.TrimSpace(handoff.Summary) == "" {
				missing = append(missing, field)
			}
		case HandoffFieldChangedFiles:
			if len(handoff.ChangedFiles) == 0 {
				missing = append(missing, field)
			}
		case HandoffFieldCommandsRun:
			if len(handoff.CommandsRun) == 0 {
				missing = append(missing, field)
			}
		case HandoffFieldVerification:
			if strings.TrimSpace(handoff.Verification) == "" {
				missing = append(missing, field)
			}
		case HandoffFieldRisks:
			if len(handoff.Risks) == 0 {
				missing = append(missing, field)
			}
		case HandoffFieldNextAction:
			if strings.TrimSpace(handoff.NextAction) == "" {
				missing = append(missing, field)
			}
		}
	}
	return missing
}

func trimUniqueList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

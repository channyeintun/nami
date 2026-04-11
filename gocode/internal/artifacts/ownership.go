package artifacts

import "strings"

const (
	MetadataSessionID      = "session_id"
	MetadataSlot           = "slot"
	MetadataOwnerScope     = "owner_scope"
	MetadataOwnerID        = "owner_id"
	MetadataOwnerAuthority = "owner_authority"

	OwnerAuthorityParentSession = "parent-session"
	OwnerAuthorityUser          = "user"
)

func normalizeOwnershipMetadata(scope Scope, metadata map[string]any, ownerID string, slot string) map[string]any {
	normalized := cloneMetadata(metadata)
	if normalized == nil {
		normalized = make(map[string]any, 4)
	}

	trimmedOwnerID := strings.TrimSpace(ownerID)
	trimmedSlot := strings.TrimSpace(slot)

	normalized[MetadataOwnerScope] = string(scope)
	switch scope {
	case ScopeSession:
		normalized[MetadataOwnerAuthority] = OwnerAuthorityParentSession
		if trimmedOwnerID != "" {
			normalized[MetadataOwnerID] = trimmedOwnerID
			normalized[MetadataSessionID] = trimmedOwnerID
		}
	case ScopeUser:
		normalized[MetadataOwnerAuthority] = OwnerAuthorityUser
		if trimmedOwnerID != "" {
			normalized[MetadataOwnerID] = trimmedOwnerID
		}
	}

	if trimmedSlot != "" {
		normalized[MetadataSlot] = trimmedSlot
	}

	return normalized
}

func sessionOwnerID(metadata map[string]any) string {
	if sessionID := metadataString(metadata, MetadataSessionID); sessionID != "" {
		return sessionID
	}
	if ownerScope := metadataString(metadata, MetadataOwnerScope); ownerScope != string(ScopeSession) {
		return ""
	}
	if ownerAuthority := metadataString(metadata, MetadataOwnerAuthority); ownerAuthority != "" && ownerAuthority != OwnerAuthorityParentSession {
		return ""
	}
	return metadataString(metadata, MetadataOwnerID)
}

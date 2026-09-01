package state

// SessionMemberEntry records one durable collaboration membership grant.
type SessionMemberEntry struct {
	IdentityID string `json:"identity_id"`
	AddedBy    string `json:"added_by,omitempty"`
	AddedAtMS  int64  `json:"added_at_ms,omitempty"`
}

// SessionSharingDoc is the durable collaboration-authorization record for a
// session. It intentionally lives outside SessionDoc.Meta so authorization
// state cannot be clobbered by free-form metadata merges.
type SessionSharingDoc struct {
	Version      int                  `json:"version"`
	SessionID    string               `json:"session_id"`
	Visibility   string               `json:"visibility,omitempty"` // "" means "shared"
	OwnerType    string               `json:"owner_type,omitempty"` // "human" (default) | "agent"
	OwnerSubject string               `json:"owner_subject,omitempty"`
	Members      []SessionMemberEntry `json:"members,omitempty"`
	UpdatedAtMS  int64                `json:"updated_at_ms"`
}

// HasMember reports whether identityID currently holds a membership grant.
func (d SessionSharingDoc) HasMember(identityID string) bool {
	for _, member := range d.Members {
		if member.IdentityID == identityID {
			return true
		}
	}
	return false
}

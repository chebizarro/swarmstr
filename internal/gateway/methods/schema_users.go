package methods

// Param schemas for the gateway users.* durable-profile surface (swarmstr-5lln).
//
// Shapes mirror OpenClaw's gateway-protocol packages/gateway-protocol/src/schema/
// users.ts (UsersList/Self/LinkEmail/SetDisplayName/SetAvatar) with the
// nostr-user-identity deviation documented in internal/gateway/userprofiles.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxUsersAvatarBase64Length mirrors OpenClaw's UsersSetAvatar avatarBase64
// maxLength (roughly a ~512 KiB decoded avatar).
const MaxUsersAvatarBase64Length = 700_000

// UsersListRequest / UsersSelfRequest take no parameters (closed empty object).
type UsersListRequest struct{}

// UsersSelfRequest is the caller-scoped self lookup (no parameters).
type UsersSelfRequest struct{}

// UsersLinkEmailRequest links an e-mail alias to an existing profile.
type UsersLinkEmailRequest struct {
	Email           string `json:"email"`
	TargetProfileID string `json:"targetProfileId"`
}

// UsersSetDisplayNameRequest sets (or clears) a profile display name.
// DisplayName is a pointer so an explicit JSON null clears the name while an
// omitted field is rejected as a missing required parameter.
type UsersSetDisplayNameRequest struct {
	ProfileID      string  `json:"profileId"`
	DisplayName    *string `json:"displayName"`
	displayNameSet bool
}

// UsersSetAvatarRequest sets a profile avatar from base64 image bytes.
type UsersSetAvatarRequest struct {
	ProfileID    string `json:"profileId"`
	Mime         string `json:"mime"`
	AvatarBase64 string `json:"avatarBase64"`
}

func (r UsersListRequest) Normalize() (UsersListRequest, error) { return r, nil }

func (r UsersSelfRequest) Normalize() (UsersSelfRequest, error) { return r, nil }

func (r UsersLinkEmailRequest) Normalize() (UsersLinkEmailRequest, error) {
	r.Email = strings.TrimSpace(r.Email)
	r.TargetProfileID = strings.TrimSpace(r.TargetProfileID)
	if r.Email == "" {
		return r, fmt.Errorf("invalid users.linkEmail params: email is required")
	}
	if r.TargetProfileID == "" {
		return r, fmt.Errorf("invalid users.linkEmail params: targetProfileId is required")
	}
	return r, nil
}

func (r UsersSetDisplayNameRequest) Normalize() (UsersSetDisplayNameRequest, error) {
	r.ProfileID = strings.TrimSpace(r.ProfileID)
	if r.ProfileID == "" {
		return r, fmt.Errorf("invalid users.setDisplayName params: profileId is required")
	}
	if !r.displayNameSet {
		return r, fmt.Errorf("invalid users.setDisplayName params: displayName is required")
	}
	return r, nil
}

// UnmarshalJSON tracks whether displayName was present so an explicit null
// (clear) is distinguishable from an omitted field (invalid). The decoder runs
// after the shared object-param alias pass, which rewrites the camelCase
// "displayName" key to snake_case "display_name".
func (r *UsersSetDisplayNameRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		ProfileID   string          `json:"profileId"`
		DisplayName json.RawMessage `json:"display_name"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	r.ProfileID = a.ProfileID
	if len(a.DisplayName) > 0 {
		r.displayNameSet = true
		if strings.TrimSpace(string(a.DisplayName)) == "null" {
			r.DisplayName = nil
		} else {
			var name string
			if err := json.Unmarshal(a.DisplayName, &name); err != nil {
				return err
			}
			r.DisplayName = &name
		}
	}
	return nil
}

func (r UsersSetAvatarRequest) Normalize() (UsersSetAvatarRequest, error) {
	r.ProfileID = strings.TrimSpace(r.ProfileID)
	r.Mime = strings.TrimSpace(r.Mime)
	if r.ProfileID == "" {
		return r, fmt.Errorf("invalid users.setAvatar params: profileId is required")
	}
	if r.Mime == "" {
		return r, fmt.Errorf("invalid users.setAvatar params: mime is required")
	}
	if r.AvatarBase64 == "" {
		return r, fmt.Errorf("invalid users.setAvatar params: avatarBase64 is required")
	}
	if len(r.AvatarBase64) > MaxUsersAvatarBase64Length {
		return r, fmt.Errorf("invalid users.setAvatar params: avatarBase64 exceeds maximum size")
	}
	return r, nil
}

// Avatar decodes the base64 avatar payload, tolerating whitespace/newlines the
// way OpenClaw's Buffer.from(..., "base64") does.
func (r UsersSetAvatarRequest) Avatar() ([]byte, error) {
	trimmed := strings.TrimSpace(r.AvatarBase64)
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		// Fall back to raw (unpadded) encoding before giving up.
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(trimmed, "="))
		if err != nil {
			return nil, fmt.Errorf("avatarBase64 must be base64")
		}
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("avatarBase64 must not be empty")
	}
	return decoded, nil
}

func DecodeUsersListParams(params json.RawMessage) (UsersListRequest, error) {
	return decodeMethodParams[UsersListRequest](params)
}

func DecodeUsersSelfParams(params json.RawMessage) (UsersSelfRequest, error) {
	return decodeMethodParams[UsersSelfRequest](params)
}

func DecodeUsersLinkEmailParams(params json.RawMessage) (UsersLinkEmailRequest, error) {
	return decodeMethodParams[UsersLinkEmailRequest](params)
}

func DecodeUsersSetDisplayNameParams(params json.RawMessage) (UsersSetDisplayNameRequest, error) {
	return decodeMethodParams[UsersSetDisplayNameRequest](params)
}

func DecodeUsersSetAvatarParams(params json.RawMessage) (UsersSetAvatarRequest, error) {
	return decodeMethodParams[UsersSetAvatarRequest](params)
}

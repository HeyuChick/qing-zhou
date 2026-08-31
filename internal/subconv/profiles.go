package subconv

import "strings"

// RoutingProfile controls only the generated routing policy. Legacy is not a
// user-facing choice: it preserves the exact pre-profile behavior for every
// subscription URL that was already imported before profiles existed.
type RoutingProfile string

const (
	ProfileLegacy   RoutingProfile = ""
	ProfileCNDirect RoutingProfile = "cn-direct"
	ProfileProxyAll RoutingProfile = "proxy-all"
)

// NormalizeRoutingProfile accepts only explicit, versioned profile names.
// Unknown values intentionally fall back to legacy instead of changing an old
// subscription's behavior because a client or user appended a typo.
func NormalizeRoutingProfile(value string) RoutingProfile {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ProfileCNDirect):
		return ProfileCNDirect
	case string(ProfileProxyAll):
		return ProfileProxyAll
	default:
		return ProfileLegacy
	}
}

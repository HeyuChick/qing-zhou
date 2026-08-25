package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"qingzhou/internal/singbox"
)

// routeIdentityMarker cannot occur in a user-chosen proxy username (the
// validator deliberately excludes '~') or in the qz_* names minted by the
// panel. That makes the suffix reversible for traffic accounting without a
// lookup table whose rows would have to follow every entitlement transition.
const routeIdentityMarker = "~qzr"

func routeIdentityName(base string, nodeID int64) string {
	return base + routeIdentityMarker + strconv.FormatInt(nodeID, 10)
}

// baseRouteIdentity maps a logical-route stats key back to the underlying
// bucket/account key. It rejects malformed suffixes so an ordinary name that
// merely contains the marker is never rewritten.
func baseRouteIdentity(name string) (string, bool) {
	i := strings.LastIndex(name, routeIdentityMarker)
	if i <= 0 {
		return name, false
	}
	id, err := strconv.ParseInt(name[i+len(routeIdentityMarker):], 10, 64)
	if err != nil || id <= 0 {
		return name, false
	}
	return name[:i], true
}

// deriveRouteUser gives one logical node a stable credential on a shared
// physical inbound. The name is what auth_user routes on; UUID/password are
// independently derived so two subscription links cannot authenticate as each
// other even though they dial the same host and port.
func deriveRouteUser(base singbox.User, nodeID int64) singbox.User {
	seed := fmt.Sprintf("qz-route:%d:%s:%s:%s", nodeID, base.Name, base.UUID, base.Password)
	h := sha256.Sum256([]byte(seed))
	return singbox.User{
		Name:     routeIdentityName(base.Name, nodeID),
		UUID:     uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String(),
		Password: hex.EncodeToString(h[:16]),
	}
}

func isRouteIdentityFor(name string, nodeID int64) bool {
	return strings.HasSuffix(name, routeIdentityMarker+strconv.FormatInt(nodeID, 10))
}

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

// routeWireCredential derives what goes over the wire independently from the
// stats name. That separation is load-bearing: a node may move from套餐 A to B,
// changing who is billed and therefore its internal auth_user name, while the
// user's imported UUID/password must remain byte-for-byte stable.
func routeWireCredential(seed string) (string, string) {
	h := sha256.Sum256([]byte(seed))
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String(), hex.EncodeToString(h[:16])
}

// deriveRouteUser gives one logical node a stable credential on a shared
// physical inbound. The current derivation intentionally excludes base.Name;
// only the user's stable secret material and node id participate.
func deriveRouteUser(base singbox.User, nodeID int64) singbox.User {
	seed := fmt.Sprintf("qz-route-v2:%d:%s:%s", nodeID, base.UUID, base.Password)
	uu, password := routeWireCredential(seed)
	return singbox.User{
		Name:     routeIdentityName(base.Name, nodeID),
		UUID:     uu,
		Password: password,
	}
}

// deriveLegacyRouteUser reproduces the pre-v2 derivation for one temporary
// credential alias, but gives it a current, reversible stats name so its traffic
// lands on the bucket that owns the node today.
func deriveLegacyRouteUser(statsBase, sourceName, clientUUID, clientSecret string, nodeID int64) singbox.User {
	seed := fmt.Sprintf("qz-route:%d:%s:%s:%s", nodeID, sourceName, clientUUID, clientSecret)
	uu, password := routeWireCredential(seed)
	return singbox.User{
		Name:     routeIdentityName(statsBase, nodeID),
		UUID:     uu,
		Password: password,
	}
}

func isRouteIdentityFor(name string, nodeID int64) bool {
	return strings.HasSuffix(name, routeIdentityMarker+strconv.FormatInt(nodeID, 10))
}

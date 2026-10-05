package api

import (
	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

func (e nodeEntry) nodeKeys() []string {
	keys := subconv.NodeKeys(e.Link)
	seen := map[string]bool{}
	for _, key := range keys {
		seen[key] = true
	}
	for _, key := range e.LegacyKeys {
		if key != "" && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}
func (e nodeEntry) disabled(disabled map[string]bool) bool {
	for _, key := range e.nodeKeys() {
		if disabled[key] {
			return true
		}
	}
	return false
}
func (a *API) applyNodePrefsCompat(u *store.User, disable, enable []string) error {
	aliases := map[string][]string{}
	for _, entry := range a.computeNodeEntries(u) {
		keys := entry.nodeKeys()
		aliases[keys[0]] = append(aliases[keys[0]], keys...)
	}
	return a.st.ApplyNodePrefsWithAliases(u.ID, disable, enable, aliases)
}

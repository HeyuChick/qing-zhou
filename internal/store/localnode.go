package store

import "encoding/json"

// The panel's own machine is monitored without a servers row (see LocalNodeID),
// which leaves nowhere to put the things an admin records about a machine:
// who it is rented from, what it costs, when it expires. Those are not
// SSH-shaped facts — they are notes about a box someone pays for — and the
// panel's own box is the one whose expiry matters most: a landing node lapsing
// costs one node, the panel lapsing costs the whole service.
//
// So they live in settings, as one JSON value rather than six keys.
const settingLocalAsset = "monitor_local_asset"

// LocalNodeAsset is the asset side of the panel's own machine: the subset of
// the servers table that still means something for a machine with no SSH
// credentials, no config path and no systemd unit to manage.
type LocalNodeAsset struct {
	Provider   string  `json:"provider"`
	Location   string  `json:"location"`
	Spec       string  `json:"spec"`
	Price      float64 `json:"price"`
	ExpiryDate int64   `json:"expiry_date"`
	Notes      string  `json:"notes"`
}

// LocalAsset returns what the admin recorded about the panel's own machine.
// A missing or unparsable value reads as "nothing recorded", which is the same
// thing an empty servers row would say.
func (s *Store) LocalAsset() LocalNodeAsset {
	var a LocalNodeAsset
	v, err := s.GetSetting(settingLocalAsset)
	if err != nil || v == "" {
		return a
	}
	_ = json.Unmarshal([]byte(v), &a)
	return a
}

// SetLocalAsset records the asset fields of the panel's own machine.
func (s *Store) SetLocalAsset(a LocalNodeAsset) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.SetSetting(settingLocalAsset, string(b))
}

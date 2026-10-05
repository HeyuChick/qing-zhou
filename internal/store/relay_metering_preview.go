package store

// PreviewRelayUserVisionServerIDs reads the proposed topology even before the
// opt-in switches are enabled. It neither probes nodes nor changes credentials.
func (s *Store) PreviewRelayUserVisionServerIDs() ([]int64, error) {
	required, _, err := relayVisionTopologyWith(s.db)
	if err != nil {
		return nil, err
	}
	return orderedVisionServerIDs(required), nil
}
